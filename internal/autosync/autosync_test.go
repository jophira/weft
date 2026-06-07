package autosync

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jophira/weft/internal/source"
)

// ── helpers ───────────────────────────────────────────────────────────────────

var epoch = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

func stateFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".sync_state.json")
}

func src(name string, autoPull bool) source.Source {
	return source.Source{Name: name, Root: "/tmp/" + name, Remote: "git@example.com/" + name, Branch: "main", AutoPull: autoPull}
}

func noop(_ source.Source) (bool, error)    { return false, nil }
func updated(_ source.Source) (bool, error) { return true, nil }
func failing(_ source.Source) (bool, error) { return false, errors.New("pull failed") }

// ── ShouldSync ────────────────────────────────────────────────────────────────

func TestShouldSync_neverSynced(t *testing.T) {
	s := State{Sources: map[string]time.Time{}}
	if !ShouldSync(s, "work", epoch, 5*time.Minute) {
		t.Error("never synced: expected true")
	}
}

func TestShouldSync_withinInterval(t *testing.T) {
	s := State{Sources: map[string]time.Time{"work": epoch.Add(-4 * time.Minute)}}
	if ShouldSync(s, "work", epoch, 5*time.Minute) {
		t.Error("synced 4m ago with 5m interval: expected false")
	}
}

func TestShouldSync_pastInterval(t *testing.T) {
	s := State{Sources: map[string]time.Time{"work": epoch.Add(-6 * time.Minute)}}
	if !ShouldSync(s, "work", epoch, 5*time.Minute) {
		t.Error("synced 6m ago with 5m interval: expected true")
	}
}

func TestShouldSync_exactlyAtInterval(t *testing.T) {
	// last + interval == now  →  not yet past, should not sync
	s := State{Sources: map[string]time.Time{"work": epoch.Add(-5 * time.Minute)}}
	if ShouldSync(s, "work", epoch, 5*time.Minute) {
		t.Error("exactly at interval boundary: expected false")
	}
}

func TestShouldSync_zeroInterval_alwaysTrue(t *testing.T) {
	s := State{Sources: map[string]time.Time{"work": epoch}}
	if !ShouldSync(s, "work", epoch, 0) {
		t.Error("zero interval: expected always true")
	}
}

func TestShouldSync_unknownSource(t *testing.T) {
	s := State{Sources: map[string]time.Time{"other": epoch}}
	if !ShouldSync(s, "work", epoch, 5*time.Minute) {
		t.Error("source not in state: expected true")
	}
}

// ── MarkSynced ────────────────────────────────────────────────────────────────

func TestMarkSynced_addsEntry(t *testing.T) {
	s := State{Sources: map[string]time.Time{}}
	out := MarkSynced(s, "work", epoch)
	if got := out.Sources["work"]; !got.Equal(epoch) {
		t.Errorf("Sources[work] = %v, want %v", got, epoch)
	}
}

func TestMarkSynced_updatesEntry(t *testing.T) {
	old := epoch.Add(-10 * time.Minute)
	s := State{Sources: map[string]time.Time{"work": old}}
	out := MarkSynced(s, "work", epoch)
	if got := out.Sources["work"]; !got.Equal(epoch) {
		t.Errorf("Sources[work] = %v, want %v", got, epoch)
	}
}

func TestMarkSynced_preservesOtherEntries(t *testing.T) {
	s := State{Sources: map[string]time.Time{"other": epoch}}
	out := MarkSynced(s, "work", epoch)
	if _, ok := out.Sources["other"]; !ok {
		t.Error("MarkSynced dropped unrelated entry")
	}
}

func TestMarkSynced_doesNotMutateOriginal(t *testing.T) {
	s := State{Sources: map[string]time.Time{}}
	_ = MarkSynced(s, "work", epoch)
	if _, ok := s.Sources["work"]; ok {
		t.Error("MarkSynced mutated the original State")
	}
}

func TestMarkSynced_nilSources(t *testing.T) {
	s := State{}
	out := MarkSynced(s, "work", epoch)
	if _, ok := out.Sources["work"]; !ok {
		t.Error("MarkSynced with nil Sources: entry not created")
	}
}

// ── ReadState / WriteState ────────────────────────────────────────────────────

func TestReadState_missingFile_returnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")
	s, err := ReadState(path)
	if err != nil {
		t.Fatalf("missing file: expected nil error, got %v", err)
	}
	if s.Sources == nil {
		t.Error("Sources map should be initialised, not nil")
	}
	if len(s.Sources) != 0 {
		t.Errorf("expected empty Sources, got %v", s.Sources)
	}
}

func TestReadState_corruptedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(path); err == nil {
		t.Error("corrupted file: expected error")
	}
}

func TestReadState_roundtrip(t *testing.T) {
	path := stateFile(t)
	in := State{Sources: map[string]time.Time{"work": epoch, "personal": epoch.Add(time.Hour)}}
	if err := WriteState(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadState(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range in.Sources {
		if got := out.Sources[name]; !got.Equal(want) {
			t.Errorf("Sources[%q] = %v, want %v", name, got, want)
		}
	}
}

func TestWriteState_createsParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "c", "state.json")
	if err := WriteState(path, State{Sources: map[string]time.Time{}}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

// ── run (core logic via package-internal access) ─────────────────────────────

func TestRun_skipsNonAutoPullSources(t *testing.T) {
	path := stateFile(t)
	called := false
	syncFn := func(_ source.Source) (bool, error) { called = true; return false, nil }

	sources := []source.Source{src("work", false)}
	_ = run(sources, path, 0, epoch, syncFn, &bytes.Buffer{})

	if called {
		t.Error("auto_pull=false: syncFn should not be called")
	}
}

func TestRun_skipsRecentlySyncedSource(t *testing.T) {
	path := stateFile(t)
	recent := epoch.Add(-1 * time.Minute)
	if err := WriteState(path, State{Sources: map[string]time.Time{"work": recent}}); err != nil {
		t.Fatal(err)
	}

	called := false
	syncFn := func(_ source.Source) (bool, error) { called = true; return false, nil }

	sources := []source.Source{src("work", true)}
	_ = run(sources, path, 5*time.Minute, epoch, syncFn, &bytes.Buffer{})

	if called {
		t.Error("recently synced: syncFn should not be called")
	}
}

func TestRun_syncsStaleSource(t *testing.T) {
	path := stateFile(t)
	old := epoch.Add(-10 * time.Minute)
	if err := WriteState(path, State{Sources: map[string]time.Time{"work": old}}); err != nil {
		t.Fatal(err)
	}

	called := false
	syncFn := func(_ source.Source) (bool, error) { called = true; return false, nil }

	sources := []source.Source{src("work", true)}
	_ = run(sources, path, 5*time.Minute, epoch, syncFn, &bytes.Buffer{})

	if !called {
		t.Error("stale source: syncFn should have been called")
	}
}

func TestRun_updatesStateAfterSuccessfulSync(t *testing.T) {
	path := stateFile(t)
	sources := []source.Source{src("work", true)}
	_ = run(sources, path, 0, epoch, noop, &bytes.Buffer{})

	s, err := ReadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Sources["work"]; !got.Equal(epoch) {
		t.Errorf("state not updated: Sources[work] = %v, want %v", got, epoch)
	}
}

func TestRun_doesNotUpdateStateOnSyncError(t *testing.T) {
	path := stateFile(t)
	sources := []source.Source{src("work", true)}
	_ = run(sources, path, 0, epoch, failing, &bytes.Buffer{})

	s, _ := ReadState(path)
	if _, ok := s.Sources["work"]; ok {
		t.Error("sync failed: state should not record a sync timestamp")
	}
}

func TestRun_continuesAfterSyncError(t *testing.T) {
	path := stateFile(t)
	calls := 0
	syncFn := func(s source.Source) (bool, error) {
		calls++
		if s.Name == "bad" {
			return false, errors.New("boom")
		}
		return false, nil
	}

	sources := []source.Source{src("bad", true), src("good", true)}
	_ = run(sources, path, 0, epoch, syncFn, &bytes.Buffer{})

	if calls != 2 {
		t.Errorf("expected 2 sync calls, got %d", calls)
	}
}

func TestRun_returnsFirstError(t *testing.T) {
	path := stateFile(t)
	sources := []source.Source{src("a", true), src("b", true)}
	err := run(sources, path, 0, epoch, failing, &bytes.Buffer{})
	if err == nil {
		t.Error("expected error from failing syncFn")
	}
}

func TestRun_silentOnNoChange(t *testing.T) {
	path := stateFile(t)
	var out bytes.Buffer
	sources := []source.Source{src("work", true)}
	_ = run(sources, path, 0, epoch, noop, &out)

	if out.Len() != 0 {
		t.Errorf("no change: expected no output, got %q", out.String())
	}
}

func TestRun_printsOnUpdate(t *testing.T) {
	path := stateFile(t)
	var out bytes.Buffer
	sources := []source.Source{src("work", true)}
	_ = run(sources, path, 0, epoch, updated, &out)

	if !strings.Contains(out.String(), "work") {
		t.Errorf("update: expected source name in output, got %q", out.String())
	}
}

func TestRun_printsErrorForFailedSource(t *testing.T) {
	path := stateFile(t)
	var out bytes.Buffer
	sources := []source.Source{src("bad", true)}
	_ = run(sources, path, 0, epoch, failing, &out)

	if !strings.Contains(out.String(), "bad") {
		t.Errorf("error: expected source name in output, got %q", out.String())
	}
}

func TestRun_multipleSourcesPartialStale(t *testing.T) {
	path := stateFile(t)
	if err := WriteState(path, State{Sources: map[string]time.Time{
		"fresh": epoch.Add(-1 * time.Minute),
		"stale": epoch.Add(-10 * time.Minute),
	}}); err != nil {
		t.Fatal(err)
	}

	var synced []string
	syncFn := func(s source.Source) (bool, error) {
		synced = append(synced, s.Name)
		return false, nil
	}

	sources := []source.Source{src("fresh", true), src("stale", true)}
	_ = run(sources, path, 5*time.Minute, epoch, syncFn, &bytes.Buffer{})

	if len(synced) != 1 || synced[0] != "stale" {
		t.Errorf("expected only 'stale' synced, got %v", synced)
	}
}

// ── DefaultStateFilePath ──────────────────────────────────────────────────────

func TestDefaultStateFilePath_nonEmpty(t *testing.T) {
	path, err := DefaultStateFilePath()
	if err != nil {
		t.Fatalf("DefaultStateFilePath: %v", err)
	}
	if path == "" {
		t.Error("DefaultStateFilePath() returned empty string")
	}
}

func TestDefaultStateFilePath_containsWeft(t *testing.T) {
	path, err := DefaultStateFilePath()
	if err != nil {
		t.Fatalf("DefaultStateFilePath: %v", err)
	}
	if !containsSubstr(path, "weft") {
		t.Errorf("DefaultStateFilePath() = %q; expected 'weft' in path", path)
	}
}

func containsSubstr(s, sub string) bool {
	return sub == "" || (len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

// ── WriteState error path ─────────────────────────────────────────────────────

func TestWriteState_writesToParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "state.json")
	s := State{Sources: map[string]time.Time{"x": epoch}}
	if err := WriteState(path, s); err != nil {
		t.Fatalf("WriteState into nested dirs: %v", err)
	}
}

// ── Run exported wrapper ──────────────────────────────────────────────────────

func TestRun_exported_noSources(t *testing.T) {
	path := stateFile(t)
	// Run with no sources should complete without error.
	err := Run(nil, path, 0, syncFnNoop, nil)
	if err != nil {
		t.Fatalf("Run with nil sources: %v", err)
	}
}

func syncFnNoop(_ source.Source) (bool, error) { return false, nil }

func TestRun_mixedAutoPull(t *testing.T) {
	path := stateFile(t)
	var synced []string
	syncFn := func(s source.Source) (bool, error) {
		synced = append(synced, s.Name)
		return false, nil
	}

	sources := []source.Source{
		src("enabled", true),
		src("disabled", false),
		src("alsoEnabled", true),
	}
	_ = run(sources, path, 0, epoch, syncFn, &bytes.Buffer{})

	for _, name := range synced {
		if name == "disabled" {
			t.Error("auto_pull=false source was synced")
		}
	}
	if len(synced) != 2 {
		t.Errorf("expected 2 sources synced, got %d: %v", len(synced), synced)
	}
}
