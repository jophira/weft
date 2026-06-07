package watch_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jophira/weft/internal/watch"
)

const shortDebounce = 40 * time.Millisecond
const waitBudget = 600 * time.Millisecond

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

func TestDebouncedTarget_DetectsFileWrite(t *testing.T) {
	dir := t.TempDir()
	ch := make(chan []watch.TargetChange, 1)
	var guard watch.ApplyGuard

	stop, err := watch.DebouncedTarget([]string{dir}, shortDebounce, &guard, func(cs []watch.TargetChange) {
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

	stop, err := watch.DebouncedTarget([]string{dir}, shortDebounce, &guard, func(cs []watch.TargetChange) {
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

	stop, err := watch.DebouncedTarget([]string{dir}, shortDebounce, &guard, func(cs []watch.TargetChange) {
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

func TestDebouncedTarget_SubdirFileDetected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	ch := make(chan []watch.TargetChange, 1)
	var guard watch.ApplyGuard

	stop, err := watch.DebouncedTarget([]string{dir}, shortDebounce, &guard, func(cs []watch.TargetChange) {
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
