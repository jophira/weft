package harness

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// advertised is one entry in the read-on-demand index weft writes into the
// instruction file of a harness that cannot execute a class natively.
type advertised struct {
	Class Class
	Name  string
	Desc  string
	Path  string // abs path to weft's own staged copy
}

// frontMatter is the subset of a staged file's YAML header the index needs.
type frontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Tools restricts which tools an agent may use. Claude Code enforces this;
	// no other harness can, which is why restricted agents are not advertised.
	Tools any `yaml:"tools"`
	// AllowedTools is the equivalent key used by command definitions.
	AllowedTools any `yaml:"allowed-tools"`
}

// maxDescLen keeps each index line to roughly one terminal row. The whole point
// of advertising is that the index is cheap enough to sit in an always-on rule
// file — the staged tree measured 134 KB against ~3 KB for its index, so
// descriptions must not creep back toward full content.
const maxDescLen = 120

// scanAdvertised walks the staged tree and returns index entries for every class
// the harness advertises. Files whose class is natively supported are excluded:
// they are being written properly and need no pointer.
//
// Entries are sorted by class then name so the emitted block is stable across
// applies — an unstable index would rewrite the instruction file on every run
// and churn the watcher.
func scanAdvertised(stagedRoot string, h Harness) ([]advertised, error) {
	if h == nil {
		return nil, nil
	}
	var out []advertised
	err := filepath.WalkDir(stagedRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(stagedRoot, path)
		if relErr != nil {
			return relErr
		}
		c := stagedClass(rel)
		cs := classSupportOf(h, c)
		if cs.Supported() || !cs.Advertise {
			return nil
		}
		// Only markdown carries the frontmatter the index reads; skill bundles
		// also ship assets (css, yaml, images) that are not entry points.
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		fm := readFrontMatter(path)
		if excludeFromIndex(c, fm) {
			return nil
		}
		out = append(out, advertised{
			Class: c,
			Name:  entryName(rel, fm),
			Desc:  truncate(fm.Description, maxDescLen),
			Path:  path,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning staged tree for advertisable files: %w", err)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Class != out[j].Class {
			return out[i].Class < out[j].Class
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// excludeFromIndex drops entries that would be unsafe or useless to advertise.
//
// An agent declaring a restricted tool set relies on the harness enforcing that
// restriction. Only Claude Code can. Advertising a "read-only reviewer" to a
// harness that cannot enforce `tools:` turns it into a prose persona running with
// the session's full tool access — a safety regression, not merely reduced
// fidelity. Those agents are withheld and reported instead.
func excludeFromIndex(c Class, fm frontMatter) bool {
	if c != ClassAgents {
		return false
	}
	return fm.Tools != nil || fm.AllowedTools != nil
}

// entryName prefers the frontmatter name, falling back to the file or bundle
// directory name. Skills are identified by their directory (skills/<name>/SKILL.md),
// everything else by filename.
func entryName(rel string, fm frontMatter) string {
	if fm.Name != "" {
		return fm.Name
	}
	slash := filepath.ToSlash(rel)
	if strings.EqualFold(filepath.Base(slash), "SKILL.md") {
		if dir := filepath.Base(filepath.Dir(slash)); dir != "." && dir != "/" {
			return dir
		}
	}
	return strings.TrimSuffix(filepath.Base(slash), filepath.Ext(slash))
}

// readFrontMatter extracts the YAML header of a markdown file. A missing or
// malformed header is not an error: the file is still advertisable by name, and
// failing an apply over a cosmetic header would be disproportionate.
func readFrontMatter(path string) frontMatter {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from WalkDir over weft's own staged tree
	if err != nil {
		return frontMatter{}
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return frontMatter{}
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return frontMatter{}
	}
	var fm frontMatter
	if err := yaml.Unmarshal([]byte(text[4:4+end]), &fm); err != nil {
		return frontMatter{}
	}
	return fm
}

// truncate shortens s to at most n runes, appending an ellipsis when it cuts.
// Rune-aware so a multi-byte character is never split mid-sequence.
// cf. Java: s.substring(0, n) — but Go strings are byte slices, so indexing by
// rune requires the conversion.
func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// advertiseBody renders the read-on-demand index appended to a harness's managed
// instruction block.
//
// Pointers target weft's own staged tree rather than another harness's config
// directory: pointing Codex at ~/.claude/agents would couple the two harnesses
// and break whenever Claude Code is absent — the coupling ADR 0002 removed for
// instructions.
func advertiseBody(entries []advertised) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Available via weft (read on demand)\n\n")
	b.WriteString("These are not loaded automatically. Read the file when a task matches its description.\n")

	var current Class
	for _, e := range entries {
		if e.Class != current {
			fmt.Fprintf(&b, "\n### %s\n", e.Class)
			current = e.Class
		}
		if e.Desc != "" {
			fmt.Fprintf(&b, "- **%s** — %s\n  `%s`\n", e.Name, e.Desc, filepath.ToSlash(e.Path))
		} else {
			fmt.Fprintf(&b, "- **%s**\n  `%s`\n", e.Name, filepath.ToSlash(e.Path))
		}
	}
	return b.String()
}
