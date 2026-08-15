package runstate_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jophira/weft/internal/runstate"
)

func TestReadCounts_absentIsNotAnError(t *testing.T) {
	got, err := runstate.ReadCounts(t.TempDir())
	if err != nil {
		t.Fatalf("ReadCounts on an empty dir: %v", err)
	}
	if got != nil {
		t.Errorf("ReadCounts = %+v, want nil when nothing has been recorded", got)
	}
}

func TestWriteCounts_roundTrips(t *testing.T) {
	dir := t.TempDir()
	want := runstate.Counts{Adoptable: 7, Conflicts: 2, Profile: "hybrid", UpdatedAt: time.Now().Truncate(time.Second)}
	if err := runstate.WriteCounts(dir, want); err != nil {
		t.Fatalf("WriteCounts: %v", err)
	}

	got, err := runstate.ReadCounts(dir)
	if err != nil {
		t.Fatalf("ReadCounts: %v", err)
	}
	if got == nil {
		t.Fatal("ReadCounts = nil after a write")
	}
	if got.Adoptable != want.Adoptable || got.Conflicts != want.Conflicts || got.Profile != want.Profile {
		t.Errorf("ReadCounts = %+v, want %+v", *got, want)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, want.UpdatedAt)
	}
}

// TestWriteCounts_leavesNoTempFiles guards the atomic write: a temp file left
// behind on every apply would slowly fill the config dir.
func TestWriteCounts_leavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		if err := runstate.WriteCounts(dir, runstate.Counts{Adoptable: i, UpdatedAt: time.Now()}); err != nil {
			t.Fatalf("WriteCounts: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("config dir holds %v, want counts.json alone", names)
	}
}

// TestReadCounts_corruptIsDropped keeps a damaged cache from failing every
// subsequent read. The counts are a display convenience, so the recovery is to
// forget them and let the next apply record fresh ones.
func TestReadCounts_corruptIsDropped(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "counts.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := runstate.ReadCounts(dir)
	if err != nil {
		t.Fatalf("ReadCounts on a corrupt file: %v", err)
	}
	if got != nil {
		t.Errorf("ReadCounts = %+v, want nil", got)
	}
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Error("corrupt counts.json was left in place, want it removed")
	}
}

// TestCounts_Stale pins the freshness window. Counts with no timestamp are stale
// by definition: nothing recorded them, so nothing vouches for them.
func TestCounts_Stale(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		c    runstate.Counts
		want bool
	}{
		{"just written", runstate.Counts{UpdatedAt: now.Add(-time.Minute)}, false},
		{"inside the window", runstate.Counts{UpdatedAt: now.Add(-23 * time.Hour)}, false},
		{"past the window", runstate.Counts{UpdatedAt: now.Add(-25 * time.Hour)}, true},
		{"no timestamp", runstate.Counts{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Stale(now, 24*time.Hour); got != tc.want {
				t.Errorf("Stale = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestWriteCounts_doesNotDisturbRunState pins that the two sidecars are
// independent files. Recording counts on a quiet watcher tick must not touch the
// watcher's own record, or a status read would report the watcher as gone.
func TestWriteCounts_doesNotDisturbRunState(t *testing.T) {
	dir := t.TempDir()
	rs := runstate.RunState{PID: os.Getpid(), Profile: "hybrid", ConfigDir: dir, StartedAt: time.Now()}
	if err := runstate.Write(dir, rs); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := runstate.WriteCounts(dir, runstate.Counts{Adoptable: 1, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("WriteCounts: %v", err)
	}

	got, err := runstate.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil || got.PID != rs.PID || got.Profile != rs.Profile {
		t.Errorf("runstate after WriteCounts = %+v, want the watcher record intact", got)
	}
}
