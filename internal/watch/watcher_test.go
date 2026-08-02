package watch_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jophira/weft/internal/watch"
)

const shortDebounce = 40 * time.Millisecond
const waitBudget = 600 * time.Millisecond

// scopeAll builds a TargetScope covering dir and every directory currently under
// it. Tests that exercise debounce, guard or dedup semantics use it so the scope
// itself never gets in the way; the scoping rules have their own tests below.
func scopeAll(t *testing.T, dir string) watch.TargetScope {
	t.Helper()
	var dirs []string
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return watch.TargetScope{Root: dir, Dirs: dirs}
}

// waitForChange blocks until ch receives a value or the budget expires.
// Returns nil when a value was received, or an error on timeout.
func waitForChange(t *testing.T, ch <-chan []watch.TargetChange) []watch.TargetChange {
	t.Helper()
	select {
	case changes := <-ch:
		return changes
	case <-time.After(waitBudget):
		t.Fatal("timed out waiting for DebouncedTarget callback")
		return nil
	}
}

// waitForRel drains change batches until one reports rel, or the budget expires.
//
// A single receive is not enough when the test creates a directory before
// writing into it: platforms differ on how that surfaces. Linux reports the new
// directory as a Create, which the watcher consumes to expand its scope, but
// Windows also reports the parent directory as modified, and that lands in a
// batch of its own before the file write is ever seen.
func waitForRel(t *testing.T, ch <-chan []watch.TargetChange, rel string) {
	t.Helper()
	var seen []watch.TargetChange
	deadline := time.After(waitBudget)
	for {
		select {
		case changes := <-ch:
			seen = append(seen, changes...)
			if slices.ContainsFunc(changes, func(c watch.TargetChange) bool { return c.Rel == rel }) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s in changes; saw %+v", rel, seen)
		}
	}
}

func TestDebouncedTarget_DetectsFileWrite(t *testing.T) {
	dir := t.TempDir()
	ch := make(chan []watch.TargetChange, 1)
	var guard watch.ApplyGuard

	stop, err := watch.DebouncedTarget([]watch.TargetScope{scopeAll(t, dir)}, shortDebounce, &guard, func(cs []watch.TargetChange) {
		ch <- cs
	})
	if err != nil {
		t.Fatalf("DebouncedTarget: %v", err)
	}
	defer stop()

	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("rules"), 0o644); err != nil {
		t.Fatal(err)
	}

	changes := waitForChange(t, ch)
	found := false
	for _, c := range changes {
		if c.Root == dir && c.Rel == "CLAUDE.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected TargetChange{Root:%s, Rel:CLAUDE.md}, got %+v", dir, changes)
	}
}

func TestDebouncedTarget_GuardSuppressesEvents(t *testing.T) {
	dir := t.TempDir()
	ch := make(chan []watch.TargetChange, 1)
	var guard watch.ApplyGuard

	stop, err := watch.DebouncedTarget([]watch.TargetScope{scopeAll(t, dir)}, shortDebounce, &guard, func(cs []watch.TargetChange) {
		ch <- cs
	})
	if err != nil {
		t.Fatalf("DebouncedTarget: %v", err)
	}
	defer stop()

	guard.Lock()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("weft write"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Wait past the debounce window — callback must not fire.
	time.Sleep(shortDebounce * 3)
	guard.Unlock()

	select {
	case got := <-ch:
		t.Errorf("callback fired while guard was active: %+v", got)
	default:
	}
}

func TestDebouncedTarget_DeduplicatesRapidChanges(t *testing.T) {
	dir := t.TempDir()
	ch := make(chan []watch.TargetChange, 4)
	var guard watch.ApplyGuard

	stop, err := watch.DebouncedTarget([]watch.TargetScope{scopeAll(t, dir)}, shortDebounce, &guard, func(cs []watch.TargetChange) {
		ch <- cs
	})
	if err != nil {
		t.Fatalf("DebouncedTarget: %v", err)
	}
	defer stop()

	// Write the same file three times in rapid succession.
	for range 3 {
		if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("v"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	changes := waitForChange(t, ch)
	// Expect exactly one entry for CLAUDE.md, not three.
	count := 0
	for _, c := range changes {
		if c.Rel == "CLAUDE.md" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 deduplicated entry, got %d: %+v", count, changes)
	}
	// No second batch should fire.
	select {
	case extra := <-ch:
		t.Errorf("unexpected second callback batch: %+v", extra)
	case <-time.After(shortDebounce * 3):
	}
}

// ── ScopeForFiles ─────────────────────────────────────────────────────────────

func TestScopeForFiles_coversManagedDirsAndAncestors(t *testing.T) {
	root := filepath.FromSlash("/home/user/.claude")
	got := watch.ScopeForFiles(root, []string{
		"CLAUDE.md",
		"commands/ship.md",
		"skills/proposal/SKILL.md",
		"skills/proposal/scripts/render.py",
	})

	if got.Root != root {
		t.Errorf("Root = %q, want %q", got.Root, root)
	}
	want := []string{
		root,
		filepath.Join(root, "commands"),
		filepath.Join(root, "skills"),
		filepath.Join(root, "skills", "proposal"),
		filepath.Join(root, "skills", "proposal", "scripts"),
	}
	for _, w := range want {
		if !slices.Contains(got.Dirs, w) {
			t.Errorf("scope is missing %q; got %v", w, got.Dirs)
		}
	}
	if len(got.Dirs) != len(want) {
		t.Errorf("Dirs = %v, want exactly %v", got.Dirs, want)
	}
}

// The whole point of the scope: a live harness home carries state directories
// weft never writes to, and they must not be watched.
func TestScopeForFiles_excludesUnmanagedSiblings(t *testing.T) {
	root := filepath.FromSlash("/home/user/.claude")
	got := watch.ScopeForFiles(root, []string{"commands/ship.md"})

	for _, unmanaged := range []string{"projects", "plugins", "session-env"} {
		if slices.Contains(got.Dirs, filepath.Join(root, unmanaged)) {
			t.Errorf("scope wrongly includes unmanaged dir %q: %v", unmanaged, got.Dirs)
		}
	}
}

func TestScopeForFiles_ignoresPathsOutsideRoot(t *testing.T) {
	root := filepath.FromSlash("/home/user/.claude")
	got := watch.ScopeForFiles(root, []string{"../.codex/AGENTS.md"})

	if len(got.Dirs) != 1 || got.Dirs[0] != root {
		t.Errorf("Dirs = %v, want just the root %q", got.Dirs, root)
	}
}

func TestScopeForFiles_noFilesYieldsRootOnly(t *testing.T) {
	root := filepath.FromSlash("/home/user/.claude")
	got := watch.ScopeForFiles(root, nil)

	if len(got.Dirs) != 1 || got.Dirs[0] != root {
		t.Errorf("Dirs = %v, want just the root %q", got.Dirs, root)
	}
}

// ── DebouncedTarget scoping ───────────────────────────────────────────────────

func TestDebouncedTarget_IgnoresDirsOutsideScope(t *testing.T) {
	root := t.TempDir()
	for _, sub := range []string{"commands", "projects"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ch := make(chan []watch.TargetChange, 1)
	var guard watch.ApplyGuard

	// Only commands/ holds managed files, so projects/ must never be watched.
	scope := watch.ScopeForFiles(root, []string{filepath.Join("commands", "ship.md")})
	stop, err := watch.DebouncedTarget([]watch.TargetScope{scope}, shortDebounce, &guard, func(cs []watch.TargetChange) {
		ch <- cs
	})
	if err != nil {
		t.Fatalf("DebouncedTarget: %v", err)
	}
	defer stop()

	if err := os.WriteFile(filepath.Join(root, "projects", "session.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ch:
		t.Errorf("callback fired for a file outside the scope: %+v", got)
	case <-time.After(shortDebounce * 3):
	}
}

// A directory created inside a managed subtree — a new skill folder — must start
// being watched without restarting the watcher.
func TestDebouncedTarget_ExpandsIntoNewManagedSubdir(t *testing.T) {
	root := t.TempDir()
	skills := filepath.Join(root, "skills", "proposal")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	// Buffered for several batches: creating the directory can produce one of
	// its own, and a blocked send would stall the watcher goroutine.
	ch := make(chan []watch.TargetChange, 8)
	var guard watch.ApplyGuard

	scope := watch.ScopeForFiles(root, []string{filepath.Join("skills", "proposal", "SKILL.md")})
	stop, err := watch.DebouncedTarget([]watch.TargetScope{scope}, shortDebounce, &guard, func(cs []watch.TargetChange) {
		ch <- cs
	})
	if err != nil {
		t.Fatalf("DebouncedTarget: %v", err)
	}
	defer stop()

	// skills/ is in scope as an ancestor, so a new folder under it is followed.
	fresh := filepath.Join(root, "skills", "newskill")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	// Give the Create event time to register the directory before writing into it.
	time.Sleep(shortDebounce * 2)
	if err := os.WriteFile(filepath.Join(fresh, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	waitForRel(t, ch, filepath.Join("skills", "newskill", "SKILL.md"))
}

// The mirror image: a directory created directly under the target root is
// harness state (projects/, plugins/), and following it is what exhausted the
// watch budget in the first place.
func TestDebouncedTarget_DoesNotExpandIntoNewRootChild(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	ch := make(chan []watch.TargetChange, 1)
	var guard watch.ApplyGuard

	scope := watch.ScopeForFiles(root, []string{filepath.Join("commands", "ship.md")})
	stop, err := watch.DebouncedTarget([]watch.TargetScope{scope}, shortDebounce, &guard, func(cs []watch.TargetChange) {
		ch <- cs
	})
	if err != nil {
		t.Fatalf("DebouncedTarget: %v", err)
	}
	defer stop()

	fresh := filepath.Join(root, "projects")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(shortDebounce * 2)
	// Drain the batch the directory creation itself produced on the root watch.
	select {
	case <-ch:
	default:
	}
	if err := os.WriteFile(filepath.Join(fresh, "session.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ch:
		t.Errorf("watcher expanded into a new root-child state directory: %+v", got)
	case <-time.After(shortDebounce * 3):
	}
}

// ── Debounced ─────────────────────────────────────────────────────────────────

func TestDebounced_callbackFires(t *testing.T) {
	dir := t.TempDir()
	ch := make(chan struct{}, 1)

	stop, err := watch.Debounced([]string{dir}, shortDebounce, func() { ch <- struct{}{} })
	if err != nil {
		t.Fatalf("Debounced: %v", err)
	}
	defer stop()

	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(waitBudget):
		t.Fatal("Debounced: timed out waiting for callback")
	}
}

func TestDebounced_noWatchableDirs_returnsError(t *testing.T) {
	_, err := watch.Debounced([]string{"/definitely/does/not/exist/xyz"}, shortDebounce, func() {})
	if err == nil {
		t.Error("Debounced with nonexistent root: expected error, got nil")
	}
}

func TestDebounced_stopIsSafe(t *testing.T) {
	dir := t.TempDir()
	stop, err := watch.Debounced([]string{dir}, shortDebounce, func() {})
	if err != nil {
		t.Fatalf("Debounced: %v", err)
	}
	stop()
	stop() // idempotent — must not panic
}

// ── DebouncedFile ───────────────────────────────────────────────────────────

func TestDebouncedFile_firesOnTargetFileChange(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(target, []byte("active_profile: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch := make(chan struct{}, 1)

	stop, err := watch.DebouncedFile(target, shortDebounce, func() { ch <- struct{}{} })
	if err != nil {
		t.Fatalf("DebouncedFile: %v", err)
	}
	defer stop()

	if err := os.WriteFile(target, []byte("active_profile: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(waitBudget):
		t.Fatal("DebouncedFile: timed out waiting for callback")
	}
}

func TestDebouncedFile_ignoresSiblingChange(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch := make(chan struct{}, 1)

	stop, err := watch.DebouncedFile(target, shortDebounce, func() { ch <- struct{}{} })
	if err != nil {
		t.Fatalf("DebouncedFile: %v", err)
	}
	defer stop()

	// A different file in the same directory must not trigger the callback.
	if err := os.WriteFile(filepath.Join(dir, "other.yaml"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
		t.Error("DebouncedFile fired on a sibling file change")
	case <-time.After(shortDebounce * 3):
	}
}

func TestDebouncedFile_survivesAtomicRewrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(target, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch := make(chan struct{}, 2)

	stop, err := watch.DebouncedFile(target, shortDebounce, func() { ch <- struct{}{} })
	if err != nil {
		t.Fatalf("DebouncedFile: %v", err)
	}
	defer stop()

	// Simulate an atomic rewrite: write a temp file then rename over the target.
	// A naive single-inode watch would stop seeing events after the first swap.
	for i := range 2 {
		tmp := filepath.Join(dir, "config.yaml.tmp")
		if err := os.WriteFile(tmp, []byte{byte('a' + i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, target); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ch:
		case <-time.After(waitBudget):
			t.Fatalf("DebouncedFile: no callback after atomic rewrite #%d", i+1)
		}
	}
}

func TestDebouncedFile_stopIsSafe(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stop, err := watch.DebouncedFile(target, shortDebounce, func() {})
	if err != nil {
		t.Fatalf("DebouncedFile: %v", err)
	}
	stop()
	stop() // idempotent — must not panic
}

func TestDebouncedTarget_SubdirFileDetected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	ch := make(chan []watch.TargetChange, 1)
	var guard watch.ApplyGuard

	stop, err := watch.DebouncedTarget([]watch.TargetScope{scopeAll(t, dir)}, shortDebounce, &guard, func(cs []watch.TargetChange) {
		ch <- cs
	})
	if err != nil {
		t.Fatalf("DebouncedTarget: %v", err)
	}
	defer stop()

	if err := os.WriteFile(filepath.Join(dir, "commands", "foo.md"), []byte("cmd"), 0o644); err != nil {
		t.Fatal(err)
	}

	changes := waitForChange(t, ch)
	found := false
	for _, c := range changes {
		if c.Rel == filepath.Join("commands", "foo.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected commands/foo.md in changes, got %+v", changes)
	}
}
