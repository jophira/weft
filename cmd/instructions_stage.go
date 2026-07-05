package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jophira/weft/internal/anchor"
	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

// expandProjectsInContent replaces the weft:projects placeholder (or a stale
// begin/end block) in content with a freshly generated snippet for srcs. When
// called with a single source it produces that source's own project list, which
// is how per-source instruction files are assembled.
func expandProjectsInContent(content string, srcs []source.Source) string {
	hasPlaceholder := strings.Contains(content, projectsPlaceholder)
	hasBlock := strings.Contains(content, projectsBegin)
	if !hasPlaceholder && !hasBlock {
		return content
	}
	snippet := generateProjectsSnippet(srcs)
	if hasPlaceholder {
		content = strings.ReplaceAll(content, projectsPlaceholder, snippet)
	}
	if hasBlock {
		content = replaceProjectsBlock(content, snippet)
	}
	return content
}

// expandSourcesInContent replaces the weft:sources placeholder (or a stale
// begin/end block) in content with a freshly generated source index for srcs.
func expandSourcesInContent(content string, srcs []source.Source, p *profile.Profile) string {
	hasPlaceholder := strings.Contains(content, sourcesPlaceholder)
	hasBlock := strings.Contains(content, sourcesBegin)
	if !hasPlaceholder && !hasBlock {
		return content
	}
	snippet := generateSourcesSnippet(srcs, p)
	switch {
	case hasPlaceholder && hasBlock:
		// Placeholder fixes the target position; remove all stale blocks first.
		for strings.Contains(content, sourcesBegin) {
			next := replaceSourcesBlock(content, "")
			if next == content {
				break
			}
			content = next
		}
		content = strings.ReplaceAll(content, sourcesPlaceholder, snippet)
	case hasPlaceholder:
		content = strings.ReplaceAll(content, sourcesPlaceholder, snippet)
	case hasBlock:
		content = replaceSourcesBlock(content, snippet)
	}
	return content
}

// stageInstructions assembles each source's instruction text into a weft-owned
// copy under instrDir (NN-<source>.md, NN = priority ordinal) and returns the
// per-source projection inputs in low→high priority order. Placeholders are
// expanded per source (projects: that source's own; sources: the full index).
func stageInstructions(roots []string, srcs []source.Source, p *profile.Profile, instrDir string) ([]harness.SourceInstruction, error) {
	if err := os.RemoveAll(instrDir); err != nil {
		return nil, fmt.Errorf("clearing instructions dir: %w", err)
	}
	if err := os.MkdirAll(instrDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating instructions dir: %w", err)
	}

	assembler := buildAssembler(roots, srcs)
	byName := sourceRootMap(srcs)
	out := make([]harness.SourceInstruction, 0, len(srcs))
	for i, s := range srcs {
		raw, err := assembler(roots[i])
		if err != nil {
			return nil, fmt.Errorf("assembling instructions for source %q: %w", s.Name, err)
		}
		content := expandProjectsInContent(string(raw), []source.Source{s})
		content = expandSourcesInContent(content, srcs, p)
		// Expand weft path anchors ({{weft.root}}, {{weft.source:NAME}}) so the
		// projected/imported instruction resolves to real paths on this machine.
		content = string(anchor.Expand([]byte(content), s.Root, byName))

		fname := fmt.Sprintf("%02d-%s.md", i, s.Name)
		path := filepath.Join(instrDir, fname)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // path under weft's own config dir
			return nil, fmt.Errorf("writing instruction copy %s: %w", fname, err)
		}
		out = append(out, harness.SourceInstruction{Name: s.Name, Content: content, CopyPath: path})
	}
	return out, nil
}
