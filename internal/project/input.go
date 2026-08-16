package project

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jophira/weft/internal/instruction"
)

// Input is one project-scoped instruction file found in a repository.
type Input struct {
	// Rel is the repo-relative path, forward-slashed, and doubles as the
	// attribution name so the reader can see which file a rule came from.
	Rel string
	// Content is what the user authored, with any weft-managed block removed.
	Content string
}

// ReadInputs collects the project instruction files matching patterns under
// root, skipping anything weft itself writes.
//
// Reading is deliberately unconstrained while writing is not. A tracked
// CLAUDE.md that weft will never modify is still a perfectly good statement of
// that repository's rules, and the whole point of a single source of truth is
// that a rule written for one harness reaches the others working in the same
// repository.
//
// exclude holds repo-relative paths weft delivers to. They are skipped outright
// rather than relying on block-stripping alone, because an inline delivery
// target also carries a preamble (Cursor's .mdc frontmatter) that is weft's
// output, not the user's input.
func ReadInputs(root string, patterns, exclude []string) ([]Input, error) {
	skip := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		skip[normaliseRel(e)] = true
	}

	seen := map[string]bool{}
	var out []Input
	for _, pattern := range patterns {
		matches, err := expandPattern(root, pattern)
		if err != nil {
			return nil, err
		}
		for _, abs := range matches {
			rel, relErr := filepath.Rel(root, abs)
			if relErr != nil {
				continue
			}
			key := normaliseRel(rel)
			if seen[key] || skip[key] {
				continue
			}
			seen[key] = true

			data, readErr := os.ReadFile(abs) //nolint:gosec // abs is matched under the repository root
			if readErr != nil {
				continue // unreadable input contributes nothing; never fatal
			}
			content := strings.TrimSpace(string(instruction.Strip(data)))
			if content == "" {
				continue // nothing the user authored
			}
			out = append(out, Input{Rel: key, Content: content})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

// expandPattern resolves one repo-relative pattern to absolute file paths.
// Patterns without glob metacharacters are stat'd directly, which keeps the
// common case free of a directory read.
func expandPattern(root, pattern string) ([]string, error) {
	full := filepath.Join(root, filepath.FromSlash(pattern))
	if !strings.ContainsAny(pattern, "*?[") {
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			return nil, nil
		}
		return []string{full}, nil
	}
	matches, err := filepath.Glob(full)
	if err != nil {
		// A malformed pattern is a weft bug, not a user error, and must not stop
		// the rest of the inputs being read.
		return nil, nil
	}
	files := matches[:0]
	for _, m := range matches {
		if info, statErr := os.Stat(m); statErr == nil && !info.IsDir() {
			files = append(files, m)
		}
	}
	sort.Strings(files)
	return files, nil
}

func normaliseRel(p string) string {
	return filepath.ToSlash(filepath.Clean(p))
}
