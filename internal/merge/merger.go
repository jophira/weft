package merge

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jophira/weft/internal/profile"
)

// Filter is a predicate applied to relative file paths. Return true to include.
type Filter func(rel string) bool

// Assembler produces the instruction content for a given source root. It is
// called in place of reading CLAUDE.md directly from disk, allowing sources
// with hierarchical instruction files to be assembled before merging.
// Return nil, nil when the root contributes no instruction content.
type Assembler func(root string) ([]byte, error)

// NamedRoot pairs a source name with its filesystem path for use in MergeRoots.
// The Name is used to generate attribution markers in the merged output when a
// file has contributions from more than one source; an empty Name disables
// attribution for that root.
type NamedRoot struct {
	Name string // human-readable source name (used in attribution comments)
	Path string // absolute filesystem path to the source root
}

// instructionFile is the canonical output path for assembled instructions.
const instructionFile = "CLAUDE.md"

// Merger applies a byte-level Strategy across a list of source root directories,
// producing a single merged tree in an output directory.
type Merger struct {
	overlay   profile.Overlay
	strategy  Strategy
	filter    Filter       // nil = include all files
	assembler Assembler    // nil = read CLAUDE.md directly from disk
	onSkip    func(string) // called with rel path when filter rejects a file; nil = no-op
}

// New creates a Merger for the given overlay mode.
func New(o profile.Overlay) *Merger {
	return &Merger{overlay: o, strategy: ForOverlay(o)}
}

// WithFilter returns a copy of the Merger that only processes files for which
// f returns true. Use this to restrict the merge to managed paths.
func (m *Merger) WithFilter(f Filter) *Merger {
	return &Merger{overlay: m.overlay, strategy: m.strategy, filter: f, assembler: m.assembler, onSkip: m.onSkip}
}

// WithAssembler returns a copy of the Merger that uses fn to produce CLAUDE.md
// content for each root instead of reading the file directly. Use this when
// source roots contain hierarchical instruction files that must be assembled
// before merging (see package collect).
func (m *Merger) WithAssembler(fn Assembler) *Merger {
	return &Merger{overlay: m.overlay, strategy: m.strategy, filter: m.filter, assembler: fn, onSkip: m.onSkip}
}

// WithSkipLogger returns a copy of the Merger that calls fn with the relative
// path of each file rejected by the filter. Use this to surface skipped files
// to the user as warnings or debug log entries.
func (m *Merger) WithSkipLogger(fn func(string)) *Merger {
	return &Merger{overlay: m.overlay, strategy: m.strategy, filter: m.filter, assembler: m.assembler, onSkip: fn}
}

// MergeRoots walks every root, collects unique relative file paths, folds each
// file through the strategy (left to right, so later roots act as the overlay),
// and writes results to outputDir. Returns a sorted manifest of written paths
// and an attribution map (rel path -> contributing root indices) for files
// assembled from more than one root.
//
// When a file has contributions from two or more roots that carry a non-empty
// Name, each source's block is wrapped with attribution comments:
//
//	<!-- weft:source:begin name="source-name" -->
//	...content...
//	<!-- weft:source:end name="source-name" -->
//
// Single-source files are written without wrappers.
// Hidden directories (e.g. .git) and hidden files are skipped.
func (m *Merger) MergeRoots(roots []NamedRoot, outputDir string) ([]string, map[string][]int, error) {
	// Collect the union of relative file paths across all roots.
	seen := map[string]struct{}{}
	// When an assembler is configured, ensure the instruction file is always
	// considered — it may not exist as a physical file in any root when sources
	// use hierarchical instruction files exclusively.
	if m.assembler != nil {
		seen[instructionFile] = struct{}{}
	}
	for _, root := range roots {
		if err := collectPaths(root.Path, seen); err != nil {
			return nil, nil, fmt.Errorf("scanning %s: %w", root.Path, err)
		}
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("creating output directory: %w", err)
	}

	var manifest []string
	attribution := map[string][]int{} // rel -> contributing root indices (populated when >1 root contributes)
	for rel := range seen {
		if m.filter != nil && !m.filter(rel) {
			if m.onSkip != nil {
				m.onSkip(rel)
			}
			continue
		}
		merged, contributors, err := m.foldFile(rel, roots)
		if err != nil {
			return nil, nil, err
		}
		if merged == nil {
			continue
		}
		dst := filepath.Join(outputDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, nil, fmt.Errorf("creating parent dir for %s: %w", rel, err)
		}
		if err := os.WriteFile(dst, merged, 0o644); err != nil {
			return nil, nil, fmt.Errorf("writing %s: %w", rel, err)
		}
		manifest = append(manifest, rel)
		if len(contributors) > 1 {
			attribution[rel] = contributors
		}
	}
	sort.Strings(manifest)
	return manifest, attribution, nil
}

// foldFile reads rel from each root and folds the contents using the strategy.
// Roots that don't have the file are skipped. Returns nil if no root has it.
// When an Assembler is configured and rel is the instruction file, the assembler
// is called instead of reading from disk. The returned []int is the set of root
// indices that contributed content (in order).
//
// When two or more roots contribute to the same file and their NamedRoot.Name is
// non-empty, each root's block is wrapped in attribution comment markers before
// folding so the assembled output clearly shows which source provided each section.
func (m *Merger) foldFile(rel string, roots []NamedRoot) ([]byte, []int, error) {
	// Pre-collect contributions so we know whether attribution markers are needed
	// before we start folding. We must read all roots first.
	type contrib struct {
		idx  int
		data []byte
	}
	var contribs []contrib
	for i, root := range roots {
		data, err := m.readContent(rel, root.Path)
		if err != nil {
			return nil, nil, err
		}
		if data == nil {
			continue
		}
		contribs = append(contribs, contrib{i, data})
	}
	if len(contribs) == 0 {
		return nil, nil, nil
	}

	multiSource := len(contribs) > 1

	var acc []byte
	var contributors []int
	for _, c := range contribs {
		contributors = append(contributors, c.idx)
		data := c.data
		if multiSource && roots[c.idx].Name != "" {
			data = wrapWithAttribution(data, roots[c.idx].Name)
		}
		if acc == nil {
			acc = data
			continue
		}
		var err error
		acc, err = m.strategy(acc, data)
		if err != nil {
			return nil, nil, fmt.Errorf("merging %s: %w", rel, err)
		}
	}
	return acc, contributors, nil
}

// wrapWithAttribution surrounds content with HTML comment markers identifying
// the source that produced it. The markers are stripped by the write-back
// normalization path before content is written back to source files.
func wrapWithAttribution(content []byte, name string) []byte {
	begin := "<!-- weft:source:begin name=" + strconv.Quote(name) + " -->\n"
	end := "\n<!-- weft:source:end name=" + strconv.Quote(name) + " -->\n"
	trimmed := bytes.TrimRight(content, "\n")
	result := make([]byte, 0, len(begin)+len(trimmed)+len(end))
	result = append(result, begin...)
	result = append(result, trimmed...)
	result = append(result, end...)
	return result
}

// readContent returns the content for rel from root. When an assembler is set
// and rel is the instruction file, the assembler is used; otherwise the file is
// read directly from disk. Returns nil, nil when the root has no content for rel.
func (m *Merger) readContent(rel, root string) ([]byte, error) {
	if rel == instructionFile && m.assembler != nil {
		data, err := m.assembler(root)
		if err != nil {
			return nil, fmt.Errorf("assembling instructions from %s: %w", root, err)
		}
		return data, nil // nil means this root contributes nothing
	}
	data, err := os.ReadFile(filepath.Join(root, rel))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s from %s: %w", rel, root, err)
	}
	return data, nil
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
