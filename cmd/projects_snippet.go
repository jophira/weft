package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/source"
)

const (
	projectsPlaceholder = "<!-- weft:projects -->"
	projectsBegin       = "<!-- weft:projects:begin — regenerated on every `weft profile use`; do not edit -->"
	projectsEnd         = "<!-- weft:projects:end -->"
)

// expandProjectsPlaceholder reads the assembled CLAUDE.md from stagedDir and
// replaces any project-rules marker with a freshly generated snippet listing
// project-rule file paths from every source that has Structure.Projects set.
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
	start := strings.Index(content, projectsBegin)
	if start < 0 {
		return content
	}
	end := strings.Index(content[start:], projectsEnd)
	if end < 0 {
		return content
	}
	end += start + len(projectsEnd)
	return content[:start] + replacement + content[end:]
}

// generateProjectsSnippet builds the <!-- weft:projects:begin/end --> block
// for all sources that declare a projects directory. For each such source it:
//   - lists every common*.md file found in the projects dir (alphabetical, always-load)
//   - appends a {project-name}.md entry for per-project rules
//
// Returns an empty begin/end block when no source declares projects.
func generateProjectsSnippet(srcs []source.Source) string {
	var lines []string

	for _, s := range srcs {
		dir := strings.TrimRight(strings.TrimSpace(s.Structure.Projects), "/\\")
		if dir == "" {
			continue
		}
		absDir := filepath.Join(s.Root, dir)

		// Enumerate common*.md files present on disk, alphabetical order.
		entries, _ := os.ReadDir(absDir)
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
				continue
			}
			if !strings.HasPrefix(e.Name(), "common") {
				continue
			}
			absPath := locate.Tilde(filepath.Join(absDir, e.Name()))
			lines = append(lines, fmt.Sprintf("   - `%s` — always", absPath))
		}

		// Per-project file — Claude substitutes {project-name} at runtime.
		projectPattern := locate.Tilde(filepath.Join(absDir, "{project-name}.md"))
		lines = append(lines, fmt.Sprintf("   - `%s` — if the file exists", projectPattern))
	}

	if len(lines) == 0 {
		return projectsBegin + "\n" + projectsEnd
	}
	return projectsBegin + "\n" + strings.Join(lines, "\n") + "\n" + projectsEnd
}
