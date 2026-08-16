package logger

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// ── ParseLevel ────────────────────────────────────────────────────────────────

func TestParseLevel_known(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{" Info ", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := ParseLevel(tt.in)
			if !ok {
				t.Fatalf("ParseLevel(%q) reported unknown, want known", tt.in)
			}
			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseLevel_unknownReportsAndDefaultsToInfo(t *testing.T) {
	got, ok := ParseLevel("verbose")
	if ok {
		t.Error("ParseLevel(\"verbose\") reported known, want unknown")
	}
	if got != slog.LevelInfo {
		t.Errorf("ParseLevel(\"verbose\") = %v, want Info as the safe fallback", got)
	}
}

// ── multi-generation rotation ─────────────────────────────────────────────────

// Rotation is the only thing standing between a chatty log and an unbounded
// file, so the chain is worth asserting end to end rather than one hop at a time.
func TestRotatingWriter_keepsGenerationChainInOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weft.log")

	// Cap at 6 bytes so each 6-byte write rotates the one before it.
	rw, err := newRotatingWriter(path, 6, 3)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	t.Cleanup(func() { _ = rw.Close() })

	for _, line := range []string{"aaaa\n", "bbbb\n", "cccc\n", "dddd\n"} {
		if _, err := rw.Write([]byte(line)); err != nil {
			t.Fatalf("Write(%q): %v", line, err)
		}
	}

	// Newest lands in the active file; each older one shifts one generation down.
	want := map[string]string{
		path:        "dddd\n",
		path + ".1": "cccc\n",
		path + ".2": "bbbb\n",
		path + ".3": "aaaa\n",
	}
	for p, content := range want {
		got, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Errorf("ReadFile(%s): %v", filepath.Base(p), readErr)
			continue
		}
		if string(got) != content {
			t.Errorf("%s = %q, want %q", filepath.Base(p), got, content)
		}
	}
}

func TestRotatingWriter_discardsBeyondLastGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weft.log")

	rw, err := newRotatingWriter(path, 6, 2)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	t.Cleanup(func() { _ = rw.Close() })

	for _, line := range []string{"aaaa\n", "bbbb\n", "cccc\n", "dddd\n"} {
		if _, err := rw.Write([]byte(line)); err != nil {
			t.Fatalf("Write(%q): %v", line, err)
		}
	}

	// Only two generations are kept, so .3 must never appear.
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Errorf("weft.log.3 exists with generations=2, want it discarded (stat err = %v)", err)
	}
	// And the oldest surviving generation is the second-newest line, not the first.
	got, err := os.ReadFile(path + ".2")
	if err != nil {
		t.Fatalf("ReadFile(.2): %v", err)
	}
	if string(got) != "bbbb\n" {
		t.Errorf("weft.log.2 = %q, want %q", got, "bbbb\n")
	}
}

func TestRotatingWriter_zeroGenerationsKeepsNoBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weft.log")

	rw, err := newRotatingWriter(path, 6, 0)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	t.Cleanup(func() { _ = rw.Close() })

	_, _ = rw.Write([]byte("aaaa\n"))
	_, _ = rw.Write([]byte("bbbb\n"))

	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Errorf("weft.log.1 exists with generations=0, want none (stat err = %v)", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "bbbb\n" {
		t.Errorf("active log = %q, want %q", got, "bbbb\n")
	}
}
