// Package collect assembles instruction content from a source root.
//
// A source may keep its AI instructions in a single file (e.g. CLAUDE.md) or
// spread across a hierarchy of domain-specific files (e.g. Backend/BACKEND.md,
// Backend/Java/JAVA.md). Collect unifies both cases into one []byte payload.
package collect

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Collect walks root and returns the concatenated content of every file that
// matches pattern, in a deterministic parent-before-child order (files before
// subdirectories at each level, alphabetical within each group).
//
// Supported patterns:
//
//   - Plain filename (no wildcards, e.g. "CLAUDE.md"):
//     reads only root/<filename>. This is the backward-compatible default.
//
//   - Glob with wildcards (e.g. "**/*.md", "*.md", "**/*INSTRUCTIONS.md"):
//     walks the full tree and matches each filename against the pattern using
//     filepath.Match. The match is on the filename alone — a pattern selects
//     files by what they are called, never by where they sit.
//
// A pattern that names a directory ("docs/**/*.md") is rejected rather than
// silently widened, which is what the old filepath.Base call did to it (#284).
// Use excludes to scope a broad pattern to part of the tree.
//
// excludes is an optional list of root-relative directory prefixes to skip
// (e.g. "commands/", "skills/") so that managed subdirectory files are not
// accidentally assembled into the instruction content.
//
// Returns nil, nil when no matching files are found.
func Collect(root, pattern string, excludes ...string) ([]byte, error) {
	if err := ValidatePattern(pattern); err != nil {
		return nil, err
	}
	if !strings.Contains(pattern, "*") {
		return readSingle(root, pattern)
	}
	return collectGlob(root, filepath.Base(pattern), normalizeExcludes(excludes))
}

// ValidatePattern reports whether pattern is one Collect can honour.
//
// Matching is by filename across the whole tree, so the only directory syntax
// that means anything is the recursive "**/" prefix — and it is a no-op, since
// the walk is recursive regardless. Anything else with a path component is a
// pattern weft cannot honour: "docs/**/*.md" was silently read as "*.md" and
// matched every .md in the source, including the ones the user meant to leave
// out. Rejecting is the honest answer, and the error names the mechanism that
// does do directory scoping.
func ValidatePattern(pattern string) error {
	if pattern == "" {
		return nil // unset — the caller substitutes the default
	}
	p := strings.TrimPrefix(filepath.ToSlash(pattern), "./")
	if !strings.Contains(strings.TrimPrefix(p, "**/"), "/") {
		return nil
	}
	return fmt.Errorf(
		"instruction glob %q names a directory, which weft cannot honour: patterns match on filename "+
			"across the whole source tree, so %q would match every file called %q wherever it sits. "+
			"Use a filename pattern (\"CLAUDE.md\", \"*.md\", \"**/*.md\") and scope it with instruction_exclude",
		pattern, pattern, filepath.Base(p))
}

// readSingle reads a single exact-named file at the root level.
func readSingle(root, name string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(root, name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

// collectGlob walks root and appends content of every file whose base name
// matches fileGlob, skipping hidden paths and excluded directories.
func collectGlob(root, fileGlob string, excludes []string) ([]byte, error) {
	var buf []byte
	err := walkFilesFirst(root, func(path string) error {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// Skip excluded directories.
		for _, ex := range excludes {
			if rel == ex || strings.HasPrefix(rel, ex+string(filepath.Separator)) {
				return nil
			}
		}
		matched, err := filepath.Match(fileGlob, filepath.Base(path))
		if err != nil || !matched {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		buf = appendSection(buf, data)
		return nil
	})
	return buf, err
}

// appendSection appends data to buf, ensuring a newline separator between
// sections. Mirrors the convention used by merge.AppendStrategy.
func appendSection(buf, data []byte) []byte {
	if len(data) == 0 {
		return buf
	}
	if len(buf) > 0 && buf[len(buf)-1] != '\n' {
		buf = append(buf, '\n')
	}
	return append(buf, data...)
}

// walkFilesFirst recursively visits path starting at root, calling fn for each
// non-hidden file. Within each directory, files are visited before
// subdirectories, and both groups are visited in alphabetical order.
func walkFilesFirst(root string, fn func(path string) error) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	// Pass 1: files in this directory.
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := fn(filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}
	// Pass 2: recurse into subdirectories (skip hidden).
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := walkFilesFirst(filepath.Join(root, e.Name()), fn); err != nil {
			return err
		}
	}
	return nil
}

// normalizeExcludes strips trailing slashes from exclude prefixes for
// consistent comparison against filepath.Rel results.
func normalizeExcludes(excludes []string) []string {
	out := make([]string, 0, len(excludes))
	for _, ex := range excludes {
		ex = strings.TrimRight(strings.TrimSpace(ex), "/\\")
		if ex != "" {
			out = append(out, ex)
		}
	}
	return out
}
