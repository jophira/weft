package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/watch"
)

// buildSrcMap constructs a name→Source lookup from a slice of sources.
// Callers that process multiple files should build this once and reuse it
// across all write-back calls to avoid redundant allocations.
// cf. Java: Map.of(entries...) or a stream Collectors.toMap — Go has no
// built-in for this, so we construct the map manually.
func buildSrcMap(srcs []source.Source) map[string]source.Source {
	m := make(map[string]source.Source, len(srcs))
	for _, s := range srcs {
		m[s.Name] = s
	}
	return m
}

// writeBackSingleSource copies the content of a changed target file back to
// the owning source root. It is a no-op (returns false) for files with
// multi-source attribution in the manifest — those are handled by the merged
// write-back path (#30). Returns (true, nil) when a write-back was performed.
func writeBackSingleSource(
	m *manifest.Manifest,
	c watch.TargetChange,
	p *profile.Profile,
	srcs []source.Source,
) (bool, error) {
	return writeBackSingleSourceMap(m, c, p, buildSrcMap(srcs))
}

// writeBackSingleSourceMap is the map-accepting variant of writeBackSingleSource.
// Use this in batch loops where the srcMap has already been built once.
func writeBackSingleSourceMap(
	m *manifest.Manifest,
	c watch.TargetChange,
	p *profile.Profile,
	srcMap map[string]source.Source,
) (bool, error) {
	// Multi-source files: skip; the merged write-back path will handle them.
	if len(m.SourceFiles[c.Rel]) > 1 {
		return false, nil
	}

	content, err := os.ReadFile(filepath.Join(c.Root, c.Rel))
	if err != nil {
		return false, fmt.Errorf("reading target file %s: %w", c.Rel, err)
	}

	srcName, srcPath, found := owningSourceFromMap(c.Rel, p, srcMap)
	if !found {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		return false, fmt.Errorf("creating source dir for %s: %w", c.Rel, err)
	}
	if err := os.WriteFile(srcPath, content, 0o644); err != nil { //nolint:gosec // srcPath is derived from source root config, not user input
		return false, fmt.Errorf("writing %s to source %s: %w", c.Rel, srcName, err)
	}
	return true, nil
}

// owningSource finds the source that should receive a write-back for rel.
// Priority: (1) source root that already has the file, (2) write_back.overrides[rel],
// (3) write_back.default. Returns ok=false when no source can be determined.
func owningSource(rel string, p *profile.Profile, srcs []source.Source) (name, absPath string, ok bool) {
	return owningSourceFromMap(rel, p, buildSrcMap(srcs))
}

// owningSourceFromMap is the map-accepting variant of owningSource.
// Use this when the srcMap has already been built for a batch of calls.
func owningSourceFromMap(rel string, p *profile.Profile, srcMap map[string]source.Source) (name, absPath string, ok bool) {
	// Prefer the source root that already contains the file.
	for _, srcName := range p.Sources {
		s, exists := srcMap[srcName]
		if !exists {
			continue
		}
		candidate := filepath.Join(s.Root, rel)
		if _, err := os.Stat(candidate); err == nil {
			return srcName, candidate, true
		}
	}

	// File is new (not in any source root). Consult write_back config.
	targetSrcName := p.WriteBack.Overrides[rel]
	if targetSrcName == "" {
		targetSrcName = p.WriteBack.Default
	}
	if targetSrcName == "" {
		return "", "", false
	}
	s, exists := srcMap[targetSrcName]
	if !exists {
		return "", "", false
	}
	return targetSrcName, filepath.Join(s.Root, rel), true
}
