package merge

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jophira/weft/internal/profile"
)

// Filter is a predicate applied to relative file paths. Return true to include.
type Filter func(rel string) bool

// Merger applies a byte-level Strategy across a list of source root directories,
// producing a single merged tree in an output directory.
type Merger struct {
	strategy Strategy
	filter   Filter // nil = include all files
}

// New creates a Merger for the given overlay mode.
func New(o profile.Overlay) *Merger {
	return &Merger{strategy: ForOverlay(o)}
}

// WithFilter returns a copy of the Merger that only processes files for which
// f returns true. Use this to restrict the merge to managed paths.
func (m *Merger) WithFilter(f Filter) *Merger {
	return &Merger{strategy: m.strategy, filter: f}
}

// MergeRoots walks every root, collects unique relative file paths, folds each
// file through the strategy (left to right, so later roots act as the overlay),
// and writes results to outputDir. Returns a sorted manifest of written paths.
//
// Hidden directories (e.g. .git) and hidden files are skipped.
func (m *Merger) MergeRoots(roots []string, outputDir string) ([]string, error) {
	// Collect the union of relative file paths across all roots.
	seen := map[string]struct{}{}
	for _, root := range roots {
		if err := collectPaths(root, seen); err != nil {
			return nil, fmt.Errorf("scanning %s: %w", root, err)
		}
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}

	var manifest []string
	for rel := range seen {
		if m.filter != nil && !m.filter(rel) {
			continue
		}
		merged, err := m.foldFile(rel, roots)
		if err != nil {
			return nil, err
		}
		if merged == nil {
			continue
		}
		dst := filepath.Join(outputDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, fmt.Errorf("creating parent dir for %s: %w", rel, err)
		}
		if err := os.WriteFile(dst, merged, 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", rel, err)
		}
		manifest = append(manifest, rel)
	}
	sort.Strings(manifest)
	return manifest, nil
}

// foldFile reads rel from each root and folds the contents using the strategy.
// Roots that don't have the file are skipped. Returns nil if no root has it.
func (m *Merger) foldFile(rel string, roots []string) ([]byte, error) {
	var acc []byte
	for _, root := range roots {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s from %s: %w", rel, root, err)
		}
		if acc == nil {
			acc = data
			continue
		}
		acc, err = m.strategy(acc, data)
		if err != nil {
			return nil, fmt.Errorf("merging %s: %w", rel, err)
		}
	}
	return acc, nil
}

// collectPaths walks root and adds each non-hidden file's relative path to seen.
// Hidden directories and files (names starting with ".") are skipped, except
// the root directory itself which may have a hidden name (e.g. ~/.claude).
func collectPaths(root string, seen map[string]struct{}) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip hidden dirs — but never skip the root itself, which may be
		// named with a leading dot (e.g. ~/.claude).
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		seen[rel] = struct{}{}
		return nil
	})
}
