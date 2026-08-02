package watch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
)

// ── targetRoot ────────────────────────────────────────────────────────────────

func TestTargetRoot_found(t *testing.T) {
	// FromSlash builds OS-native paths so targetRoot's separator-based prefix
	// match works on Windows (where filepath.Separator is "\").
	claude := filepath.FromSlash("/home/user/.claude")
	roots := []string{claude, filepath.FromSlash("/home/user/.codex")}
	root, rel, ok := targetRoot(roots, filepath.Join(claude, "CLAUDE.md"))
	if !ok {
		t.Fatal("targetRoot: expected ok=true")
	}
	if root != claude {
		t.Errorf("root = %q, want %q", root, claude)
	}
	if rel != "CLAUDE.md" {
		t.Errorf("rel = %q, want CLAUDE.md", rel)
	}
}

func TestTargetRoot_exactRoot(t *testing.T) {
	claude := filepath.FromSlash("/home/user/.claude")
	roots := []string{claude}
	root, _, ok := targetRoot(roots, claude)
	if !ok {
		t.Fatal("targetRoot on exact root path: expected ok=true")
	}
	if root != claude {
		t.Errorf("root = %q, want %q", root, claude)
	}
}

func TestTargetRoot_notFound(t *testing.T) {
	roots := []string{filepath.FromSlash("/home/user/.claude")}
	_, _, ok := targetRoot(roots, filepath.FromSlash("/home/user/.codex/CLAUDE.md"))
	if ok {
		t.Error("targetRoot outside any root: expected ok=false")
	}
}

func TestTargetRoot_emptyRoots(t *testing.T) {
	_, _, ok := targetRoot(nil, filepath.FromSlash("/some/path"))
	if ok {
		t.Error("targetRoot with nil roots: expected ok=false")
	}
}

func TestTargetRoot_subdir(t *testing.T) {
	claude := filepath.FromSlash("/home/user/.claude")
	roots := []string{claude}
	_, rel, ok := targetRoot(roots, filepath.Join(claude, "commands", "foo.md"))
	if !ok {
		t.Fatal("targetRoot in subdir: expected ok=true")
	}
	want := filepath.Join("commands", "foo.md")
	if rel != want {
		t.Errorf("rel = %q, want %q", rel, want)
	}
}

// ── watchSet ──────────────────────────────────────────────────────────────────

// newTestWatchSet returns a source-kind set whose watcher is closed on cleanup.
func newTestWatchSet(t *testing.T) *watchSet {
	t.Helper()
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return newWatchSet(w, "source", sourceHint)
}

func TestWatchSetAddTree_countsDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub1", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub2"), 0o755); err != nil {
		t.Fatal(err)
	}

	set := newTestWatchSet(t)
	if err := set.addTree(dir); err != nil {
		t.Fatalf("addTree: %v", err)
	}
	// expect 4: root, sub1, sub1/nested, sub2
	if set.len() != 4 {
		t.Errorf("watched = %d, want 4", set.len())
	}
}

func TestWatchSetAddTree_skipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".hidden", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "visible"), 0o755); err != nil {
		t.Fatal(err)
	}

	set := newTestWatchSet(t)
	if err := set.addTree(dir); err != nil {
		t.Fatalf("addTree: %v", err)
	}
	// root + visible = 2; .hidden and its sub are skipped
	if set.len() != 2 {
		t.Errorf("watched = %d, want 2 (hidden dirs skipped)", set.len())
	}
}

func TestWatchSetAddTree_deduplicatesAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	set := newTestWatchSet(t)
	for range 3 {
		if err := set.addTree(dir); err != nil {
			t.Fatalf("addTree: %v", err)
		}
	}
	// Re-adding must not consume ceiling budget for directories already watched.
	if set.len() != 2 {
		t.Errorf("watched = %d, want 2 after repeated addTree", set.len())
	}
}

func TestWatchSetAddTree_limitExceeded(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	set := newTestWatchSet(t)
	set.limit = 2 // root+a+b+c = 4 dirs; should error
	if err := set.addTree(dir); err == nil {
		t.Error("addTree past the ceiling: expected error, got nil")
	}
}

func TestWatchSetAddTree_nonexistentRoot(t *testing.T) {
	set := newTestWatchSet(t)

	// Non-existent root — WalkDir reports an error for the root itself, but
	// addTree silently skips unreadable paths, so nothing is watched and no
	// error surfaces.
	if err := set.addTree(filepath.FromSlash("/definitely/does/not/exist")); err != nil {
		t.Fatalf("addTree nonexistent root: unexpected error: %v", err)
	}
	if set.len() != 0 {
		t.Errorf("watched = %d, want 0", set.len())
	}
}

// ── expandTargetScope ─────────────────────────────────────────────────────────

func TestExpandTargetScope_followsSubdirOfManagedDir(t *testing.T) {
	root := t.TempDir()
	skills := filepath.Join(root, "skills")
	fresh := filepath.Join(skills, "newskill")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}

	set := newTestWatchSet(t)
	set.add(root)
	set.add(skills)

	expandTargetScope(set, []string{root}, fresh)
	if !set.has(fresh) {
		t.Errorf("expected %q to be watched after expansion", fresh)
	}
}

func TestExpandTargetScope_refusesDirectChildOfRoot(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "projects")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}

	set := newTestWatchSet(t)
	set.add(root)

	expandTargetScope(set, []string{root}, state)
	if set.has(state) {
		t.Errorf("expanded into %q, a harness state directory under the root", state)
	}
}

func TestExpandTargetScope_ignoresDirsOutsideScope(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	set := newTestWatchSet(t)
	set.add(root)

	expandTargetScope(set, []string{root}, outside)
	if set.has(outside) {
		t.Errorf("expanded into %q, which is outside every scope", outside)
	}
}
