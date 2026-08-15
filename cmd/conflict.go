package cmd

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/manifest"
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

// detectApplyConflicts finds the files two or more harnesses have diverged on
// and reports them, returning the paths each harness must leave alone.
func detectApplyConflicts(stagedDir, cfgDir string, out io.Writer) (map[string]map[string]bool, error) {
	targets, err := conflictTargets(cfgDir)
	if err != nil {
		return nil, err
	}
	if len(targets) < 2 {
		return nil, nil // one harness cannot disagree with itself
	}
	conflicts, err := harness.DetectConflicts(stagedDir, targets)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 && out != nil {
		harness.FormatConflicts(out, conflicts, time.Now())
	}
	return harness.HeldPaths(conflicts), nil
}
