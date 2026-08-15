package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"time"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/runstate"
)

// conflictTargets builds the detection set: every harness with a recorded target
// root, whether or not the active profile still lists it. A harness dropped from
// the profile keeps its files on disk, and an edit made there still competes for
// the same source.
func conflictTargets(cfgDir string) ([]harness.ConflictTarget, error) {
	reg := harness.NewRegistry(harness.Instances()...)
	names := manifestHarnessNames(cfgDir)
	sort.Strings(names)

	targets := make([]harness.ConflictTarget, 0, len(names))
	for _, name := range names {
		m, err := manifest.Load(cfgDir, name)
		if err != nil {
			return nil, fmt.Errorf("loading manifest for %s: %w", name, err)
		}
		if m.TargetRoot == "" {
			continue // never applied as a file tree — nothing to compare
		}
		h, _ := reg.Get(name) // nil for a harness with no adapter; routing falls back to the staged layout
		targets = append(targets, harness.ConflictTarget{
			Harness: name, Root: m.TargetRoot, H: h, Manifest: m,
		})
	}
	return targets, nil
}

// recordStatusCounts caches the numbers `weft status --short` shows, so a
// harness status line rendering once per turn reads a file instead of walking
// every harness root (ADR 0004).
//
// A failure here is logged and swallowed. The counts are a display convenience;
// refusing an apply because a cache could not be written would trade something
// the user asked for against something they did not.
func recordStatusCounts(cfgDir, profileName string, conflicts int) {
	adoptable, err := adoptableCount(cfgDir)
	if err != nil {
		slog.Warn("adoptable scan for status counts failed", slog.Any("error", err))
		return
	}
	c := runstate.Counts{
		Adoptable: adoptable,
		Conflicts: conflicts,
		Profile:   profileName,
		UpdatedAt: time.Now(),
	}
	if wErr := runstate.WriteCounts(cfgDir, c); wErr != nil {
		slog.Warn("writing status counts failed", slog.Any("error", wErr))
	}
}

// adoptableCount counts harness-native files no source owns, using the same scan
// `weft adopt --scan` lists.
func adoptableCount(cfgDir string) (int, error) {
	targets, err := adoptTargets(cfgDir)
	if err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil
	}
	candidates, err := harness.Scan(targets)
	if err != nil {
		return 0, err
	}
	return len(candidates), nil
}

// detectApplyConflicts finds the files two or more harnesses have diverged on
// and reports them, returning the paths each harness must leave alone and how
// many canonical files are held. The count is the number of conflicts, not the
// number of held paths: one conflict freezes a copy in every harness that
// diverged, so counting paths would report the same disagreement more than once.
func detectApplyConflicts(stagedDir, cfgDir string, out io.Writer) (map[string]map[string]bool, int, error) {
	targets, err := conflictTargets(cfgDir)
	if err != nil {
		return nil, 0, err
	}
	if len(targets) < 2 {
		return nil, 0, nil // one harness cannot disagree with itself
	}
	conflicts, err := harness.DetectConflicts(stagedDir, targets)
	if err != nil {
		return nil, 0, err
	}
	if len(conflicts) > 0 && out != nil {
		harness.FormatConflicts(out, conflicts, time.Now())
	}
	return harness.HeldPaths(conflicts), len(conflicts), nil
}
