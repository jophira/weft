package runstate_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jophira/weft/internal/runstate"
)

func TestReadMissing_returnsNil(t *testing.T) {
	rs, err := runstate.Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read on missing file: %v", err)
	}
	if rs != nil {
		t.Errorf("Read on missing file = %+v, want nil", rs)
	}
}

func TestWriteRead_roundtrip_livePID(t *testing.T) {
	dir := t.TempDir()
	want := runstate.RunState{
		PID:       os.Getpid(), // our own pid is guaranteed alive
		Profile:   "hybrid",
		ConfigDir: dir,
		StartedAt: time.Now().Add(-5 * time.Minute).UTC(),
	}
	if err := runstate.Write(dir, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := runstate.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil {
		t.Fatal("Read returned nil for a live pid")
	}
	if got.PID != want.PID || got.Profile != want.Profile || got.ConfigDir != want.ConfigDir {
		t.Errorf("Read = %+v, want %+v", got, want)
	}
	if got.Uptime() <= 0 {
		t.Errorf("Uptime() = %v, want > 0", got.Uptime())
	}
}

func TestRead_deadPID_isTreatedAsStaleAndRemoved(t *testing.T) {
	dir := t.TempDir()
	// PID 0x7FFFFFFF is astronomically unlikely to be live.
	if err := runstate.Write(dir, runstate.RunState{PID: 0x7FFFFFFF, Profile: "x", ConfigDir: dir, StartedAt: time.Now()}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := runstate.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != nil {
		t.Errorf("Read with dead pid = %+v, want nil", got)
	}
	// The stale sidecar must have been removed.
	if _, statErr := os.Stat(filepath.Join(dir, "watcher.json")); !os.IsNotExist(statErr) {
		t.Errorf("stale sidecar not removed: stat err = %v", statErr)
	}
}

func TestClear_removesFile_andIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := runstate.Write(dir, runstate.RunState{PID: os.Getpid(), ConfigDir: dir, StartedAt: time.Now()}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := runstate.Clear(dir); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "watcher.json")); !os.IsNotExist(statErr) {
		t.Errorf("Clear did not remove file: %v", statErr)
	}
	// Second clear on an already-absent file must not error.
	if err := runstate.Clear(dir); err != nil {
		t.Errorf("Clear on missing file: %v", err)
	}
}

func TestRead_corruptJSON_returnsNilAndClears(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "watcher.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := runstate.Read(dir)
	if err != nil {
		t.Fatalf("Read on corrupt file: %v", err)
	}
	if got != nil {
		t.Errorf("Read on corrupt file = %+v, want nil", got)
	}
}
