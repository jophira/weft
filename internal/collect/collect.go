// Package collect assembles instruction content from a source root.
//
// A source may keep its AI instructions in a single file (e.g. CLAUDE.md) or
// spread across a hierarchy of domain-specific files (e.g. Backend/BACKEND.md,
// Backend/Java/JAVA.md). Collect unifies both cases into one []byte payload.
package collect

import (
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
//     walks the full tree and matches each filename against the last path
//     component of the pattern using filepath.Match.
//
// excludes is an optional list of root-relative directory prefixes to skip
// (e.g. "commands/", "skills/") so that managed subdirectory files are not
// accidentally assembled into the instruction content.
//
// Returns nil, nil when no matching files are found.
func Collect(root, pattern string, excludes ...string) ([]byte, error) {
	if !strings.Contains(pattern, "*") {
		return readSingle(root, pattern)
	}
	return collectGlob(root, filepath.Base(pattern), normalizeExcludes(excludes))
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
