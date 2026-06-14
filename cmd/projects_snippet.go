package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/source"
)

const (
	projectsPlaceholder = "<!-- weft:projects -->"
	projectsBegin       = "<!-- weft:projects:begin — regenerated on every `weft profile use`; do not edit -->"
	projectsEnd         = "<!-- weft:projects:end -->"

	// maxProjectDepth is the maximum directory depth searched when discovering
	// project roots and enumerating project files. Prevents runaway walks on
	// unexpectedly deep source trees.
	maxProjectDepth = 10
)

// expandProjectsPlaceholder reads the assembled CLAUDE.md from stagedDir and
// replaces any project-rules marker with a freshly generated snippet listing
// project-rule file paths from every source.
//
// Two forms are recognised:
//   - Raw placeholder  <!-- weft:projects -->          (canonical source form)
//   - Existing block   <!-- weft:projects:begin -->    (written by a previous apply,
//     ...               possibly propagated back to the source by write-back)
//     <!-- weft:projects:end -->
//
// Both are replaced with a freshly generated begin/end block so that the
// snippet is always current regardless of what write-back has done to the source.
func expandProjectsPlaceholder(stagedDir string, srcs []source.Source) error {
	claudePath := filepath.Join(stagedDir, "CLAUDE.md")
	data, err := os.ReadFile(claudePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading staged CLAUDE.md: %w", err)
	}

	content := string(data)
	hasPlaceholder := strings.Contains(content, projectsPlaceholder)
	hasBlock := strings.Contains(content, projectsBegin)
	if !hasPlaceholder && !hasBlock {
		return nil
	}

	snippet := generateProjectsSnippet(srcs)

	// Replace the raw placeholder first (canonical case).
	if hasPlaceholder {
		content = strings.ReplaceAll(content, projectsPlaceholder, snippet)
	}

	// Replace any existing begin/end block (write-back propagated case).
	if hasBlock {
		content = replaceProjectsBlock(content, snippet)
	}

	return os.WriteFile(claudePath, []byte(content), 0o644) //nolint:gosec // claudePath is derived from weft's own staged dir, not user input
}

// replaceProjectsBlock replaces the first <!-- weft:projects:begin -->...
// <!-- weft:projects:end --> block in content with replacement.
// Returns content unchanged if the block is malformed or end marker is missing.
func replaceProjectsBlock(content, replacement string) string {
	return replacePlaceholderBlock(content, projectsBegin, projectsEnd, replacement)
}

// generateProjectsSnippet builds the <!-- weft:projects:begin/end --> block
// for all sources. For each source it:
//   - auto-discovers any directory whose base name matches the source's
//     EffectiveProjectDirNames anywhere in the source tree (up to maxProjectDepth)
//   - also honours the legacy explicit Structure.Projects path
//   - recursively enumerates all .md files under each discovered project root
//     (up to maxProjectDepth levels) and lists them as explicit absolute paths
//
// Entries are grouped by project root so that the language/category (encoded
// in the parent path, e.g. php/project-rules/) is visually clear to the AI.
//
// Returns an empty begin/end block when no project files are found.
func generateProjectsSnippet(srcs []source.Source) string {
	type group struct {
		root  string
		files []string
	}
	var groups []group

	for _, s := range srcs {
		nameSet := buildNameSet(s.Structure.EffectiveProjectDirNames())

		// Auto-discover project roots anywhere in the source tree.
		projectRoots, _ := findProjectRoots(s.Root, nameSet)

		// Honour the legacy explicit Projects path, deduplicating if already found.
		if explicit := strings.TrimRight(strings.TrimSpace(s.Structure.Projects), "/\\"); explicit != "" {
			absExplicit := filepath.Join(s.Root, explicit)
			if !slices.Contains(projectRoots, absExplicit) {
				projectRoots = append(projectRoots, absExplicit)
			}
		}
		sort.Strings(projectRoots)

		for _, root := range projectRoots {
			files, err := collectProjectFiles(root)
			if err != nil || len(files) == 0 {
				continue
			}
			groups = append(groups, group{root: root, files: files})
		}
	}

	if len(groups) == 0 {
		return projectsBegin + "\n" + projectsEnd
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", projectsBegin)
	sb.WriteString("When working in a project, find the matching entry below and read its rule file(s):\n")
	for _, g := range groups {
		fmt.Fprintf(&sb, "\n`%s/`:\n", locate.Tilde(g.root))
		for _, f := range g.files {
			fmt.Fprintf(&sb, "   - `%s`\n", locate.Tilde(f))
		}
	}
	sb.WriteString(projectsEnd)
	return sb.String()
}

// findProjectRoots walks sourceRoot up to maxProjectDepth levels and returns
// the absolute paths of every directory whose base name is in nameSet.
// Matched directories are not descended into — their contents are handled by
// collectProjectFiles.
func findProjectRoots(sourceRoot string, nameSet map[string]bool) ([]string, error) {
	if _, err := os.Stat(sourceRoot); os.IsNotExist(err) {
		return nil, nil
	}
	var roots []string
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		if depth > maxProjectDepth {
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil // skip unreadable dirs silently
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			child := filepath.Join(dir, e.Name())
			if nameSet[e.Name()] {
				roots = append(roots, child)
				// don't recurse into matched project roots
			} else {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return roots, walk(sourceRoot, 0)
}

// collectProjectFiles recursively collects all .md files under root up to
// maxProjectDepth levels, in lexical order.
func collectProjectFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries silently
		}
		if path == root {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			if strings.Count(rel, string(filepath.Separator)) >= maxProjectDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(d.Name()) == ".md" {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
