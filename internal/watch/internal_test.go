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

// ── addRecursive ──────────────────────────────────────────────────────────────

func TestAddRecursive_countsDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub1", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub2"), 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	count, err := addRecursive(w, dir, maxWatchDirs)
	if err != nil {
		t.Fatalf("addRecursive: %v", err)
	}
	// expect 4: root, sub1, sub1/nested, sub2
	if count != 4 {
		t.Errorf("addRecursive count = %d, want 4", count)
	}
}

func TestAddRecursive_skipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".hidden", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "visible"), 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	count, err := addRecursive(w, dir, maxWatchDirs)
	if err != nil {
		t.Fatalf("addRecursive: %v", err)
	}
	// root + visible = 2; .hidden and its sub are skipped
	if count != 2 {
		t.Errorf("addRecursive count = %d, want 2 (hidden dirs skipped)", count)
	}
}

func TestAddRecursive_limitExceeded(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Limit=2 but root+a+b+c = 4 dirs; should error.
	_, err = addRecursive(w, dir, 2)
	if err == nil {
		t.Error("addRecursive with limit exceeded: expected error, got nil")
	}
}

func TestAddRecursive_nonexistentRoot(t *testing.T) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Non-existent root — WalkDir returns an error for the root itself,
	// but addRecursive silently skips unreadable paths so count=0, err=nil.
	count, err := addRecursive(w, "/definitely/does/not/exist", maxWatchDirs)
	if err != nil {
		t.Fatalf("addRecursive nonexistent root: unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("addRecursive nonexistent root count = %d, want 0", count)
	}
}
