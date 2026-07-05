// Package pathlint detects and heals path references in weft source files.
//
// Rule/command/agent files accumulate hardcoded, home-relative path references
// (e.g. ~/workspace/ai-rules/java/x.md, /Users/me/..., malformed @.~/...) that
// break when a source is cloned elsewhere or its path doesn't match. Because
// weft knows where every source lives, it can classify these references and
// rewrite the healable ones to the portable {{weft.root}} / {{weft.source:NAME}}
// anchors (see package anchor).
package pathlint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jophira/weft/internal/anchor"
)

// Kind classifies a path reference finding.
type Kind string

const (
	// HardcodedInSource: the reference resolves to a file inside a registered
	// source but via an absolute/home path — non-portable. Healable.
	HardcodedInSource Kind = "hardcoded-in-source"
	// StalePrefix: the reference does not resolve, but a unique file with the
	// same trailing path exists inside a source. Healable.
	StalePrefix Kind = "stale-prefix"
	// BrokenAnchor: an anchored reference expands to a path that does not exist.
	BrokenAnchor Kind = "broken-anchor"
	// UnresolvedAnchor: a {{weft.source:NAME}} reference names an unknown source.
	UnresolvedAnchor Kind = "unresolved-anchor"
	// ExternalPath: the reference resolves to a file outside every source; it
	// cannot be anchored. Report-only.
	ExternalPath Kind = "external-path"
	// DeadReference: the reference resolves nowhere and has no unique candidate.
	DeadReference Kind = "dead-reference"
)

// Finding is one path reference that needs attention.
type Finding struct {
	Source     string // owning source name
	File       string // absolute path of the file containing the reference
	Line       int    // 1-based line number
	Ref        string // the raw reference text as written
	Kind       Kind
	Suggestion string // replacement (anchor form); empty when not healable
}

// Fixable reports whether the finding has a safe automatic rewrite.
func (f Finding) Fixable() bool { return f.Suggestion != "" }

// Actionable reports whether the finding needs a decision: it is either
// auto-healable or an anchor that doesn't resolve. External and dead
// references are informational (often intentional) and excluded.
func (f Finding) Actionable() bool {
	return f.Fixable() || f.Kind == BrokenAnchor || f.Kind == UnresolvedAnchor
}

// Source is a registered source to scan.
type Source struct {
	Name string
	Root string
}

// textExts are the file extensions scanned for references.
var textExts = map[string]bool{
	".md": true, ".markdown": true, ".txt": true,
	".sh": true, ".yaml": true, ".yml": true, ".json": true,
}

// refExts are the extensions a raw (non-anchored) path reference must end in to
// be considered — this bounds false positives from prose.
var refExts = map[string]bool{
	".md": true, ".markdown": true, ".txt": true,
	".sh": true, ".yaml": true, ".yml": true, ".json": true, ".pdf": true,
}

// tokenRe captures path-ish tokens preceded by a boundary (start of line or a
// delimiter) so it doesn't match a slash in the middle of a word (e.g. the
// "/import" inside "scripts/import"). Group 1 is an optional leading @, then an
// anchor, a home path, an absolute path, or a relative path.
var tokenRe = regexp.MustCompile("(?:^|[\\s(\\[{\"'`>])(@?(?:\\{\\{weft\\.[^}]+\\}\\}|\\.~/|~/|\\.{1,2}/|/)[^\\s`\"'()\\[\\]]*)")

// weftAnchorRe matches a full weft anchor token, used to detect foreign
// template braces ({lang}, {TICKET-ID}) that should not be linted.
var weftAnchorRe = regexp.MustCompile(`\{\{weft\.[^}]+\}\}`)

// normSource resolves a source root to an absolute, symlink-evaluated path so
// containment checks are reliable even when ~/.claude etc. are symlinks.
type normSource struct {
	name string
	root string
}

type indexedFile struct {
	abs    string
	srcIdx int
	rel    string
}

// Scan walks every source and returns findings for all path references that are
// hardcoded, stale, broken, or dead. Sources that don't exist on disk are
// skipped silently.
func Scan(srcs []Source) ([]Finding, error) {
	norm := make([]normSource, 0, len(srcs))
	for _, s := range srcs {
		root := evalSymlinks(s.Root)
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		norm = append(norm, normSource{name: s.Name, root: root})
	}

	index, err := buildIndex(norm)
	if err != nil {
		return nil, err
	}

	var findings []Finding
	for _, f := range index {
		if !textExts[strings.ToLower(filepath.Ext(f.abs))] {
			continue
		}
		data, err := os.ReadFile(f.abs) //nolint:gosec // path from walking a registered source root
		if err != nil {
			continue
		}
		findings = append(findings, scanFile(f, norm, index, data)...)
	}
	return findings, nil
}

// buildIndex lists every non-hidden file across all source roots.
func buildIndex(norm []normSource) ([]indexedFile, error) {
	var index []indexedFile
	for i, s := range norm {
		err := filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") && path != s.root {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			rel, relErr := filepath.Rel(s.root, path)
			if relErr != nil {
				return nil
			}
			index = append(index, indexedFile{abs: path, srcIdx: i, rel: filepath.ToSlash(rel)})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return index, nil
}

// scanFile extracts and classifies references from one file's content.
func scanFile(f indexedFile, norm []normSource, index []indexedFile, data []byte) []Finding {
	var out []Finding
	dir := filepath.Dir(f.abs)
	for lineNo, line := range strings.Split(string(data), "\n") {
		seen := map[string]bool{} // dedupe identical tokens within a line
		for _, m := range tokenRe.FindAllStringSubmatch(line, -1) {
			raw := m[1]
			ref := cleanToken(raw)
			if ref == "" {
				continue
			}
			if seen[raw] {
				continue
			}
			seen[raw] = true
			if fnd, ok := classify(ref, f, dir, norm, index); ok {
				fnd.Line = lineNo + 1
				// Report and rewrite the token exactly as written (including a
				// leading @ and any malformed ".~/"), so Apply replaces the real
				// text and the @import prefix is preserved.
				fnd.Ref = raw
				if fnd.Suggestion != "" && strings.HasPrefix(raw, "@") {
					fnd.Suggestion = "@" + fnd.Suggestion
				}
				out = append(out, fnd)
			}
		}
	}
	return out
}

// cleanToken strips a leading @, normalises the malformed ".~/" prefix to "~/",
// and trims trailing markdown punctuation. Returns "" for tokens that are not
// worth classifying.
func cleanToken(raw string) string {
	t := strings.TrimPrefix(raw, "@")
	// Malformed "@.~/" / ".~/" → "~/".
	if strings.HasPrefix(t, ".~/") {
		t = t[1:]
	}
	t = strings.TrimRight(t, ".,;:")
	if t == "" || !strings.Contains(t, "/") {
		return ""
	}
	return t
}

// classify determines the finding kind and suggestion for a reference, or
// reports ok=false when the reference is fine (resolves through an anchor or a
// portable relative path).
func classify(ref string, f indexedFile, dir string, norm []normSource, index []indexedFile) (Finding, bool) {
	self := norm[f.srcIdx]
	base := Finding{Source: self.name, File: f.abs}

	// Skip foreign template placeholders like {lang} or {TICKET-ID}: they are
	// not concrete paths.
	if hasForeignBrace(ref) {
		return Finding{}, false
	}

	// Anchored references: already portable — only report when they don't resolve.
	if anchor.Has([]byte(ref)) {
		return classifyAnchored(ref, self, norm, base)
	}

	// Raw path: resolve to an absolute path.
	abs, ok := resolveRaw(ref, dir)
	if !ok {
		return Finding{}, false
	}
	hasRefExt := refExts[strings.ToLower(filepath.Ext(ref))]

	if _, err := os.Stat(abs); err == nil { //nolint:gosec // existence check on a reference path, not a file open
		// Exists. Portable if it lives inside a source; otherwise external.
		if name, rel, in := owningSource(abs, norm); in {
			base.Kind = HardcodedInSource
			base.Suggestion = anchorRef(self.name, name, rel)
			return base, true
		}
		// Existing but external: only report file-like refs, not bare dirs/paths
		// (e.g. ~/.claude/commands) that are legitimately outside every source.
		if !hasRefExt {
			return Finding{}, false
		}
		base.Kind = ExternalPath
		return base, true
	}

	// Does not resolve. Only file-like references (with a known extension) are
	// worth reporting — this filters out slash-command mentions like /ship.
	if !hasRefExt {
		return Finding{}, false
	}
	if name, rel, ok := uniqueSuffixMatch(ref, norm, index); ok {
		base.Kind = StalePrefix
		base.Suggestion = anchorRef(self.name, name, rel)
		return base, true
	}
	base.Kind = DeadReference
	return base, true
}

// hasForeignBrace reports whether ref contains template braces that are not
// part of a weft anchor (e.g. {lang}, {project}, {TICKET-ID}).
func hasForeignBrace(ref string) bool {
	return strings.ContainsAny(weftAnchorRe.ReplaceAllString(ref, ""), "{}")
}

// classifyAnchored handles references that already use a weft anchor.
func classifyAnchored(ref string, self normSource, norm []normSource, base Finding) (Finding, bool) {
	byName := make(map[string]string, len(norm))
	for _, s := range norm {
		byName[s.name] = s.root
	}
	// Unknown named source?
	for _, m := range regexp.MustCompile(`\{\{weft\.source:([^}]+)\}\}`).FindAllStringSubmatch(ref, -1) {
		if _, ok := byName[strings.TrimSpace(m[1])]; !ok {
			base.Kind = UnresolvedAnchor
			return base, true
		}
	}
	expanded := string(anchor.Expand([]byte(ref), self.root, byName))
	if anchor.Has([]byte(expanded)) {
		return Finding{}, false // still unresolved token we don't own — leave it
	}
	if _, err := os.Stat(expanded); err != nil { //nolint:gosec // existence check on an expanded anchor path, not a file open
		base.Kind = BrokenAnchor
		return base, true
	}
	return Finding{}, false // resolves fine
}

// resolveRaw turns a raw reference into an absolute path. Home (~) is expanded;
// relative refs resolve against dir. Reports ok=false for refs that only make
// sense as anchors.
func resolveRaw(ref, dir string) (string, bool) {
	switch {
	case strings.HasPrefix(ref, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		return filepath.Join(home, ref[2:]), true
	case strings.HasPrefix(ref, "/"):
		return filepath.Clean(ref), true
	case strings.HasPrefix(ref, "./"), strings.HasPrefix(ref, "../"):
		if !refExts[strings.ToLower(filepath.Ext(ref))] {
			return "", false
		}
		return filepath.Clean(filepath.Join(dir, ref)), true
	default:
		return "", false
	}
}

// owningSource reports whether abs lives inside one of the sources, returning
// that source's name and the file's path relative to the source root.
func owningSource(abs string, norm []normSource) (name, rel string, ok bool) {
	abs = evalSymlinks(abs)
	for _, s := range norm {
		if r, err := filepath.Rel(s.root, abs); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return s.name, filepath.ToSlash(r), true
		}
	}
	return "", "", false
}

// uniqueSuffixMatch finds a source file whose path ends with the longest unique
// trailing segment of ref. Returns the owning source name and the file's rel
// path when exactly one file matches the most specific (longest) non-empty
// suffix.
func uniqueSuffixMatch(ref string, norm []normSource, index []indexedFile) (name, rel string, ok bool) {
	segs := splitSegments(ref)
	if len(segs) == 0 {
		return "", "", false
	}
	for k := len(segs); k >= 1; k-- {
		tail := strings.Join(segs[len(segs)-k:], "/")
		var matches []indexedFile
		for _, f := range index {
			if f.rel == tail || strings.HasSuffix(f.rel, "/"+tail) {
				matches = append(matches, f)
			}
		}
		if len(matches) == 0 {
			continue // shorten the tail
		}
		if len(matches) == 1 {
			m := matches[0]
			return norm[m.srcIdx].name, m.rel, true
		}
		return "", "", false // ambiguous at the most specific level; shorter only worsens
	}
	return "", "", false
}

// splitSegments returns the path segments of ref, dropping a leading ~, empty,
// and "." / ".." components.
func splitSegments(ref string) []string {
	ref = strings.TrimPrefix(ref, "~/")
	var segs []string
	for s := range strings.SplitSeq(ref, "/") {
		if s == "" || s == "." || s == ".." || s == "~" {
			continue
		}
		segs = append(segs, s)
	}
	return segs
}

// anchorRef builds the anchored replacement for a reference: {{weft.root}} when
// the target lives in the same source as the file being fixed, otherwise
// {{weft.source:NAME}}.
func anchorRef(selfName, targetName, rel string) string {
	if selfName == targetName {
		return anchor.RootToken + "/" + rel
	}
	return "{{weft.source:" + targetName + "}}/" + rel
}

// evalSymlinks resolves symlinks in path, falling back to a cleaned path when
// resolution fails (e.g. the path does not exist).
func evalSymlinks(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// Apply rewrites the healable findings in place, replacing each Ref with its
// Suggestion on the recorded line. Returns the number of files changed.
func Apply(findings []Finding) (int, error) {
	byFile := map[string][]Finding{}
	for _, f := range findings {
		if f.Fixable() {
			byFile[f.File] = append(byFile[f.File], f)
		}
	}
	changed := 0
	for file, fs := range byFile {
		data, err := os.ReadFile(file) //nolint:gosec // path from a registered source root
		if err != nil {
			return changed, err
		}
		lines := strings.Split(string(data), "\n")
		dirty := false
		for _, f := range fs {
			if f.Line < 1 || f.Line > len(lines) {
				continue
			}
			i := f.Line - 1
			if strings.Contains(lines[i], f.Ref) {
				lines[i] = strings.ReplaceAll(lines[i], f.Ref, f.Suggestion)
				dirty = true
			}
		}
		if dirty {
			if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o644); err != nil { //nolint:gosec // rewriting a source file in place
				return changed, err
			}
			changed++
		}
	}
	return changed, nil
}
