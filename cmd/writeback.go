package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	if err := os.WriteFile(srcPath, normalizeForSource(content), 0o644); err != nil { //nolint:gosec // srcPath is derived from source root config, not user input
		return false, fmt.Errorf("writing %s to source %s: %w", c.Rel, srcName, err)
	}
	return true, nil
}

// normalizeForSource prepares harness file content for writing back to a source.
// Generated placeholder expansions are collapsed to their compact placeholder form,
// and source attribution markers are stripped — both belong only in the assembled
// harness output, not in source files.
func normalizeForSource(content []byte) []byte {
	s := string(content)
	s = replaceSourcesBlock(s, sourcesPlaceholder)
	s = replaceProjectsBlock(s, projectsPlaceholder)
	s = stripAttributionMarkers(s)
	return []byte(s)
}

// stripAttributionMarkers removes <!-- weft:source:begin ... --> and
// <!-- weft:source:end ... --> lines from s.
func stripAttributionMarkers(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<!-- weft:source:begin") ||
			strings.HasPrefix(trimmed, "<!-- weft:source:end") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// dispatchWriteBack attempts single-source write-back first, then falls back to
// the merged write-back path for files with multiple contributing sources.
// Returns (true, nil) when at least one source was updated.
//
// On success the manifest's hash for the file is refreshed to the bytes now on
// disk. Callers must persist the manifest afterwards — see refreshManifestHash
// for why skipping this produces a spurious conflict.
func dispatchWriteBack(m *manifest.Manifest, c watch.TargetChange, p *profile.Profile, srcMap map[string]source.Source) (bool, error) {
	performed, err := writeBackSingleSourceMap(m, c, p, srcMap)
	if err != nil {
		return false, err
	}
	if !performed && len(m.SourceFiles[c.Rel]) > 1 {
		performed, err = writeBackMergedSourceMap(m, c, p, srcMap)
		if err != nil {
			return false, err
		}
	}
	if !performed {
		return false, nil
	}
	if err := refreshManifestHash(m, c); err != nil {
		// The write-back itself succeeded and the source file is safe; only the
		// bookkeeping failed. Report it as performed so the caller does not treat
		// the edit as lost, but surface the error so the cause is visible.
		return true, err
	}
	return true, nil
}

// refreshManifestHash re-points the manifest entry for c.Rel at the bytes now on
// disk in the target, marking the file as reconciled with its source.
//
// Without this the manifest still holds the pre-edit hash after a write-back. The
// apply that follows compares the target against that stale hash, sees a
// mismatch, and declares the file externally modified — backing up a file it just
// reconciled and then writing back byte-identical content. The apply is supposed
// to be a no-op at that point.
//
// Note this hashes the *target* bytes, not what was written to the source: apply
// compares against the target, and normalizeForSource means the two legitimately
// differ (placeholder collapsing, attribution markers).
func refreshManifestHash(m *manifest.Manifest, c watch.TargetChange) error {
	full := filepath.Join(c.Root, c.Rel)
	hash, err := manifest.HashFile(full)
	if err != nil {
		return fmt.Errorf("refreshing manifest hash for %s after write-back: %w", c.Rel, err)
	}
	m.Files[c.Rel] = hash
	return nil
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
