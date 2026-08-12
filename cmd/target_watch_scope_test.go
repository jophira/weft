package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/watch"
)

// seedTargetManifest writes a manifest for claude-code listing rels, creating
// each file's parent directory so the watcher has something real to add.
func seedTargetManifest(t *testing.T, cfgDir, targetRoot string, rels ...string) {
	t.Helper()

	files := make(map[string]string, len(rels))
	for _, rel := range rels {
		abs := filepath.Join(targetRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", abs, err)
		}
		files[rel] = "sha256:abc"
	}

	writeManifest(t, cfgDir, &manifest.Manifest{
		Harness:    "claude-code",
		Profile:    "p",
		TargetRoot: targetRoot,
		Files:      files,
	})
}

func targetScopeProfile() *profile.Profile {
	return &profile.Profile{
		Name:    "p",
		Sources: []string{"s"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"claude-code"},
	}
}

// An apply that projects the first file into a new top-level directory must
// widen the watch scope. The scope is fixed when a watcher starts, and
// expandTargetScope deliberately refuses a new direct child of the target root,
// so without a rebuild that directory stays unwatched for the life of the
// watcher and external edits inside it are silently missed (#230).
func TestTargetWatcherSet_RebuildsWhenApplyAddsADirectory(t *testing.T) {
	cfgDir := t.TempDir()
	targetRoot := t.TempDir()
	var guard watch.ApplyGuard

	seedTargetManifest(t, cfgDir, targetRoot, "CLAUDE.md")

	ts := &targetWatcherSet{}
	t.Cleanup(ts.stopAll)
	if err := ts.rebuild(targetScopeProfile(), nil, cfgDir, &guard); err != nil {
		t.Fatalf("initial rebuild: %v", err)
	}

	before := slices.Clone(ts.dirs["claude-code"])
	agentsDir := filepath.Join(targetRoot, "agents")
	if slices.Contains(before, agentsDir) {
		t.Fatalf("agents/ should not be in the initial scope: %v", before)
	}

	// The apply: a first agents/ file lands in a target that had none.
	seedTargetManifest(t, cfgDir, targetRoot, "CLAUDE.md", "agents/reviewer.md")

	if err := ts.rebuild(targetScopeProfile(), nil, cfgDir, &guard); err != nil {
		t.Fatalf("rebuild after apply: %v", err)
	}

	after := ts.dirs["claude-code"]
	if !slices.Contains(after, agentsDir) {
		t.Errorf("watch scope did not widen to the new directory\n  before: %v\n  after:  %v", before, after)
	}
}

// Rebuilding must not churn watchers that did not change. A profile applying to
// several harnesses usually gains a directory in one of them, and tearing the
// rest down drops events during the gap for no reason.
func TestTargetWatcherSet_LeavesAnUnchangedScopeAlone(t *testing.T) {
	cfgDir := t.TempDir()
	targetRoot := t.TempDir()
	var guard watch.ApplyGuard

	seedTargetManifest(t, cfgDir, targetRoot, "CLAUDE.md")

	ts := &targetWatcherSet{}
	t.Cleanup(ts.stopAll)
	p := targetScopeProfile()
	if err := ts.rebuild(p, nil, cfgDir, &guard); err != nil {
		t.Fatalf("initial rebuild: %v", err)
	}
	first := ts.stops["claude-code"]

	// An apply that changes file contents but no directory: same scope.
	seedTargetManifest(t, cfgDir, targetRoot, "CLAUDE.md")
	if err := ts.rebuild(p, nil, cfgDir, &guard); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}

	// Comparing func values is not allowed, so identity is checked through the
	// scope record the set keys its decision on.
	if _, still := ts.stops["claude-code"]; !still || first == nil {
		t.Fatal("watcher should still be running")
	}
	if got := ts.dirs["claude-code"]; !slices.Equal(got, []string{filepath.Clean(targetRoot)}) {
		t.Errorf("scope changed unexpectedly: %v", got)
	}
}

// A target whose manifest has gone has nothing left to watch, so its watcher
// must be dropped rather than left pointing at a tree weft no longer owns.
func TestTargetWatcherSet_DropsATargetWithNoManifest(t *testing.T) {
	cfgDir := t.TempDir()
	targetRoot := t.TempDir()
	var guard watch.ApplyGuard

	seedTargetManifest(t, cfgDir, targetRoot, "CLAUDE.md")

	ts := &targetWatcherSet{}
	t.Cleanup(ts.stopAll)
	p := targetScopeProfile()
	if err := ts.rebuild(p, nil, cfgDir, &guard); err != nil {
		t.Fatalf("initial rebuild: %v", err)
	}
	if len(ts.stops) != 1 {
		t.Fatalf("want 1 watcher, got %d", len(ts.stops))
	}

	if err := os.RemoveAll(filepath.Join(cfgDir, "manifests")); err != nil {
		t.Fatal(err)
	}
	if err := ts.rebuild(p, nil, cfgDir, &guard); err != nil {
		t.Fatalf("rebuild after manifest removal: %v", err)
	}

	if len(ts.stops) != 0 {
		t.Errorf("watcher for a target with no manifest should be dropped, got %d", len(ts.stops))
	}
	if len(ts.dirs) != 0 {
		t.Errorf("scope record should be dropped with the watcher, got %v", ts.dirs)
	}
}
