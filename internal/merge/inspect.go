package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jophira/weft/internal/profile"
)

// FileEntry describes one file's presence across source roots.
type FileEntry struct {
	Rel        string   // relative file path
	Roots      []string // roots containing this file, in source order
	WinnerRoot string   // root that wins under the strategy; "" means all sources contribute (merge)
}

// InspectReport is the result of an Inspect dry-run.
type InspectReport struct {
	Entries []FileEntry
	Overlay profile.Overlay
}

// Conflicts returns entries present in more than one root.
func (r *InspectReport) Conflicts() []FileEntry {
	var out []FileEntry
	for _, e := range r.Entries {
		if len(e.Roots) > 1 {
			out = append(out, e)
		}
	}
	return out
}

// Unique returns entries present in exactly one root.
func (r *InspectReport) Unique() []FileEntry {
	var out []FileEntry
	for _, e := range r.Entries {
		if len(e.Roots) == 1 {
			out = append(out, e)
		}
	}
	return out
}

// Inspect performs a dry-run: walks all roots, classifies files by conflict
// status, and determines which root wins under the current strategy.
// Nothing is written to disk.
//
// When an Assembler is configured, CLAUDE.md is always included in the seen
// set (mirroring MergeRoots) and each root's presence is determined by calling
// the assembler rather than os.Stat — roots that produce non-empty assembled
// content are counted as present even when no physical CLAUDE.md file exists.
func (m *Merger) Inspect(roots []string) (*InspectReport, error) {
	seen := map[string]struct{}{}
	// When an assembler is wired, the instruction file is always a candidate —
	// it may not exist on disk but will be synthesised from glob-matched files.
	if m.assembler != nil {
		seen[instructionFile] = struct{}{}
	}
	for _, root := range roots {
		if err := collectPaths(root, seen); err != nil {
			return nil, fmt.Errorf("scanning %s: %w", root, err)
		}
	}

	var entries []FileEntry
	for rel := range seen {
		if m.filter != nil && !m.filter(rel) {
			continue
		}
		var present []string
		for _, root := range roots {
			if rel == instructionFile && m.assembler != nil {
				// Use the assembler to determine whether this root contributes
				// instruction content — a root with instruction_glob may have no
				// physical CLAUDE.md but still produce non-empty assembled output.
				data, err := m.assembler(root)
				if err != nil {
					return nil, fmt.Errorf("assembling instructions from %s: %w", root, err)
				}
				if len(data) > 0 {
					present = append(present, root)
				}
			} else {
				if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
					present = append(present, root)
				}
			}
		}
		if len(present) == 0 {
			continue
		}
		var winner string
		switch {
		case len(present) == 1:
			winner = present[0]
		case m.overlay == profile.OverlayMerge:
			winner = "" // all sources contribute; no single winner
		default:
			winner = present[len(present)-1] // cascade / last-wins: last overlay wins
		}
		entries = append(entries, FileEntry{
			Rel:        rel,
			Roots:      present,
			WinnerRoot: winner,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Rel < entries[j].Rel })

	return &InspectReport{Entries: entries, Overlay: m.overlay}, nil
}
