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

// expandProjectsPlaceholder reads the assembled CLAUDE.md from stagedDir,
// finds any <!-- weft:projects --> placeholder, and replaces it with a
// generated snippet listing the project-rule file paths from every source
// that has Structure.Projects set. A no-op when the placeholder is absent.
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
	if !strings.Contains(content, projectsPlaceholder) {
		return nil
	}

	snippet := generateProjectsSnippet(srcs)
	expanded := strings.ReplaceAll(content, projectsPlaceholder, snippet)

	return os.WriteFile(claudePath, []byte(expanded), 0o644) //nolint:gosec // claudePath is derived from weft's own staged dir, not user input
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
