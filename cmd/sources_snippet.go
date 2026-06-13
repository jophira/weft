package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

const (
	sourcesPlaceholder = "<!-- weft:sources -->"
	sourcesBegin       = "<!-- weft:sources:begin — regenerated on every `weft profile use`; do not edit -->"
	sourcesEnd         = "<!-- weft:sources:end -->"

	maxSourceDepth = 10
)

// stackCond describes how to detect a technology stack from the repo root and
// what human-readable condition to print in the snippet.
type stackCond struct {
	files []string // one of these filenames must exist in the repo root
	label string   // condition text, e.g. "`go.mod` is present"
}

// knownStacks maps dev/<dirname>/ to its detection condition.
// Directories not listed here fall back to always-load (safe default).
var knownStacks = map[string]stackCond{
	"go":     {files: []string{"go.mod"}, label: "`go.mod` is present"},
	"java":   {files: []string{"pom.xml", "build.gradle", "build.gradle.kts"}, label: "`pom.xml` or `build.gradle` is present"},
	"python": {files: []string{"pyproject.toml", "setup.py", "setup.cfg"}, label: "`pyproject.toml` or `setup.py` is present"},
	"vue":    {files: []string{"package.json"}, label: "`package.json` is present"},
	"node":   {files: []string{"package.json"}, label: "`package.json` is present"},
	"rust":   {files: []string{"Cargo.toml"}, label: "`Cargo.toml` is present"},
	"ruby":   {files: []string{"Gemfile"}, label: "`Gemfile` is present"},
	"php":    {files: []string{"composer.json"}, label: "`composer.json` is present"},
}

// sourceFile is a discovered .md file with its resolved load condition.
type sourceFile struct {
	abs       string // absolute path on disk
	condition string // empty = always load; non-empty = human-readable condition label
}

// expandSourcesPlaceholder reads the assembled CLAUDE.md from stagedDir and
// replaces any sources marker with a freshly generated snippet that lists all
// .md files from every source, grouped by their load condition.
//
// Two forms are recognised:
//   - Raw placeholder  <!-- weft:sources -->          (canonical source form)
//   - Existing block   <!-- weft:sources:begin -->    (written by a previous apply,
//     ...               possibly propagated back to the source by write-back)
//     <!-- weft:sources:end -->
//
// Both are replaced so the snippet is always current regardless of what
// write-back has done to the source file.
func expandSourcesPlaceholder(stagedDir string, srcs []source.Source, p *profile.Profile) error {
	claudePath := filepath.Join(stagedDir, "CLAUDE.md")
	data, err := os.ReadFile(claudePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading staged CLAUDE.md: %w", err)
	}

	content := string(data)
	hasPlaceholder := strings.Contains(content, sourcesPlaceholder)
	hasBlock := strings.Contains(content, sourcesBegin)
	if !hasPlaceholder && !hasBlock {
		return nil
	}

	snippet := generateSourcesSnippet(srcs, p)

	switch {
	case hasPlaceholder && hasBlock:
		// Placeholder defines the target position; all existing blocks are stale.
		// Remove every block first, then expand the placeholder in one pass.
		for strings.Contains(content, sourcesBegin) {
			next := replaceSourcesBlock(content, "")
			if next == content {
				break // safety: stop if no progress
			}
			content = next
		}
		content = strings.ReplaceAll(content, sourcesPlaceholder, snippet)
	case hasPlaceholder:
		content = strings.ReplaceAll(content, sourcesPlaceholder, snippet)
	case hasBlock:
		content = replaceSourcesBlock(content, snippet)
	}

	return os.WriteFile(claudePath, []byte(content), 0o644) //nolint:gosec // claudePath is derived from weft's own staged dir, not user input
}

// replaceSourcesBlock replaces the first <!-- weft:sources:begin -->...
// <!-- weft:sources:end --> block in content with replacement.
// Returns content unchanged when either marker is absent or malformed.
func replaceSourcesBlock(content, replacement string) string {
	start := strings.Index(content, sourcesBegin)
	if start < 0 {
		return content
	}
	end := strings.Index(content[start:], sourcesEnd)
	if end < 0 {
		return content
	}
	end += start + len(sourcesEnd)
	return content[:start] + replacement + content[end:]
}

// generateSourcesSnippet builds the <!-- weft:sources:begin/end --> block for
// all sources. Files are classified as always-load or conditionally-load based
// on their path relative to each source root:
//
//   - Root-level .md files and dev/common*/... and dev/doc/... → always load
//   - dev/<known-stack>/...                                    → conditional on stack
//   - dev/<unknown-dir>/... and non-dev subdirs                → always load (safe default)
//
// Conditional groups are output in alphabetical order by condition label.
// A write-back instruction pointing to the primary source is prepended.
func generateSourcesSnippet(srcs []source.Source, p *profile.Profile) string {
	var alwaysFiles []string
	condMap := map[string][]string{} // condition label → []tilde-path

	for _, s := range srcs {
		projectNameSet := buildNameSet(s.Structure.EffectiveProjectDirNames())
		files, err := collectSourceFiles(s.Root, s.Structure.ManagedDirs(), projectNameSet)
		if err != nil {
			continue
		}
		for _, f := range files {
			tilde := locate.Tilde(f.abs)
			if f.condition == "" {
				alwaysFiles = append(alwaysFiles, tilde)
			} else {
				condMap[f.condition] = append(condMap[f.condition], tilde)
			}
		}
	}

	if len(alwaysFiles) == 0 && len(condMap) == 0 {
		return sourcesBegin + "\n" + sourcesEnd
	}

	condLabels := make([]string, 0, len(condMap))
	for label := range condMap {
		condLabels = append(condLabels, label)
	}
	sort.Strings(condLabels)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", sourcesBegin)

	if primaryPath := resolvePrimarySource(srcs, p); primaryPath != "" {
		fmt.Fprintf(&sb, "To edit any rule, write directly to the source file — never edit this file.\n")
		fmt.Fprintf(&sb, "Primary source for edits: `%s`\n\n", primaryPath)
	}

	if len(alwaysFiles) > 0 {
		sb.WriteString("Always read:\n")
		for _, f := range alwaysFiles {
			fmt.Fprintf(&sb, "- `%s`\n", f)
		}
	}

	for _, cond := range condLabels {
		fmt.Fprintf(&sb, "\nWhen %s, also read:\n", cond)
		for _, f := range condMap[cond] {
			fmt.Fprintf(&sb, "- `%s`\n", f)
		}
	}

	sb.WriteString(sourcesEnd)
	return sb.String()
}

// resolvePrimarySource returns the tilde-normalised root path of the primary
// write-back source. It prefers p.WriteBack.Default (matched by source name),
// then falls back to the first source.
func resolvePrimarySource(srcs []source.Source, p *profile.Profile) string {
	if p != nil && p.WriteBack.Default != "" {
		for _, s := range srcs {
			if s.Name == p.WriteBack.Default {
				return locate.Tilde(s.Root)
			}
		}
	}
	if len(srcs) > 0 {
		return locate.Tilde(srcs[0].Root)
	}
	return ""
}

// collectSourceFiles walks sourceRoot and returns all .md files, excluding:
//   - Hidden files and directories (names starting with ".")
//   - Top-level directories in managedDirs (commands/, skills/, etc.)
//   - Any directory whose base name is in projectDirNames (handled by weft:projects)
//   - Files deeper than maxSourceDepth directory levels
//
// Each returned sourceFile carries the load condition from classifySourceFile.
func collectSourceFiles(sourceRoot string, managedDirs []string, projectDirNames map[string]bool) ([]sourceFile, error) {
	if _, err := os.Stat(sourceRoot); os.IsNotExist(err) {
		return nil, nil
	}

	managed := make(map[string]bool, len(managedDirs))
	for _, d := range managedDirs {
		if d = strings.TrimRight(strings.TrimSpace(d), "/\\"); d != "" {
			managed[d] = true
		}
	}

	var files []sourceFile
	err := filepath.WalkDir(sourceRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries silently
		}
		if path == sourceRoot {
			return nil
		}

		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(sourceRoot, path)

		if d.IsDir() {
			depth := strings.Count(rel, string(filepath.Separator))
			if depth >= maxSourceDepth {
				return filepath.SkipDir
			}
			// Top-level managed dirs are skipped entirely.
			if depth == 0 && managed[d.Name()] {
				return filepath.SkipDir
			}
			// Project dirs at any depth are skipped (handled by weft:projects).
			if projectDirNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		if filepath.Ext(d.Name()) != ".md" {
			return nil
		}

		files = append(files, sourceFile{
			abs:       path,
			condition: classifySourceFile(rel),
		})
		return nil
	})
	return files, err
}

// classifySourceFile returns the load condition for a .md file based on its
// path relative to the source root.
//
// Convention:
//   - Root-level files               → always load ("")
//   - dev/common*/...                → always load
//   - dev/doc/...                    → always load
//   - dev/<known-stack>/...          → conditional on stack detection
//   - dev/<unknown-dir>/...          → always load (safe default)
//   - Any other top-level subdirs    → always load
func classifySourceFile(rel string) string {
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 3)
	if len(parts) < 2 {
		// Root-level file.
		return ""
	}
	if parts[0] != "dev" {
		// Non-dev directory — always load.
		return ""
	}
	subdir := parts[1]
	if strings.HasPrefix(subdir, "common") || subdir == "doc" {
		return ""
	}
	if cond, ok := knownStacks[subdir]; ok {
		return cond.label
	}
	// Unknown stack directory — always load (safe default).
	return ""
}
