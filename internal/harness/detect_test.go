package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/testenv"
)

// fakeBinary puts an executable named name on a temp PATH and returns its path.
func fakeBinary(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("binary detection uses PATHEXT on Windows; covered on Unix")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return p
}

// detectable is every adapter that declares detection signals, with the binary
// each one is expected to look for. Warp is absent deliberately: it declares no
// binary, and the table asserts binary fallback.
func detectableHarnesses() []struct {
	h      Harness
	binary string
	dir    string
} {
	return []struct {
		h      Harness
		binary string
		dir    string
	}{
		{&ClaudeCode{}, "claude", ".claude"},
		{&Codex{}, "codex", ".codex"},
		{&Cursor{}, "cursor", ".cursor"},
		{&GeminiCLI{}, "gemini", ".gemini"},
		{&Windsurf{}, "windsurf", filepath.Join(".codeium", "windsurf")},
		{&Aider{}, "aider", ".aider"},
	}
}

// TestDetect_ConfigDirSignal covers the case a config directory exists but the
// binary does not — the shape that held before binary fallbacks were added.
func TestDetect_ConfigDirSignal(t *testing.T) {
	for _, tc := range detectableHarnesses() {
		t.Run(tc.h.Name(), func(t *testing.T) {
			home := t.TempDir()
			testenv.SetHome(t, home)
			testenv.ClearPath(t)
			if err := os.MkdirAll(filepath.Join(home, tc.dir), 0o755); err != nil {
				t.Fatal(err)
			}

			if !tc.h.Detect() {
				t.Fatalf("%s: config dir %s exists but Detect returned false", tc.h.Name(), tc.dir)
			}
			via, detail := tc.h.(DetectReporter).DetectedVia()
			if via != DetectConfigDir {
				t.Errorf("via = %v, want DetectConfigDir", via)
			}
			if !strings.Contains(detail, filepath.ToSlash(tc.dir)) {
				t.Errorf("detail = %q, want it to name %s", detail, tc.dir)
			}
		})
	}
}

// TestDetect_BinarySignal is the regression this work exists for: a harness
// installed with its config directory absent must still be found, and must
// report the binary as the evidence rather than a directory nobody checked.
func TestDetect_BinarySignal(t *testing.T) {
	for _, tc := range detectableHarnesses() {
		t.Run(tc.h.Name(), func(t *testing.T) {
			testenv.SetHome(t, t.TempDir()) // empty home — no config dir
			bin := fakeBinary(t, tc.binary)

			if !tc.h.Detect() {
				t.Fatalf("%s: %s on PATH but Detect returned false", tc.h.Name(), tc.binary)
			}
			via, detail := tc.h.(DetectReporter).DetectedVia()
			if via != DetectBinary {
				t.Fatalf("via = %v, want DetectBinary", via)
			}
			if !strings.HasSuffix(detail, tc.binary) {
				t.Errorf("detail = %q, want the path to %s (%s)", detail, tc.binary, bin)
			}
		})
	}
}

// TestDetect_NoSignals asserts a clean machine reports nothing found, and that
// DetectedVia does not leak a stale root from a previous probe.
func TestDetect_NoSignals(t *testing.T) {
	for _, tc := range detectableHarnesses() {
		t.Run(tc.h.Name(), func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			testenv.ClearPath(t)

			if tc.h.Detect() {
				t.Fatalf("%s: nothing installed but Detect returned true", tc.h.Name())
			}
			if via, detail := tc.h.(DetectReporter).DetectedVia(); via != DetectNone || detail != "" {
				t.Errorf("DetectedVia = (%v, %q), want (DetectNone, \"\")", via, detail)
			}
		})
	}
}

// TestDetect_ConfigDirWinsOverBinary pins the priority order. The directory is
// the more specific signal: it names the exact root to write into, whereas the
// binary only proves the tool exists somewhere.
func TestDetect_ConfigDirWinsOverBinary(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	fakeBinary(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &ClaudeCode{}
	if !c.Detect() {
		t.Fatal("Detect returned false with both signals present")
	}
	if via, _ := c.DetectedVia(); via != DetectConfigDir {
		t.Errorf("via = %v, want DetectConfigDir to take priority", via)
	}
}

// TestAiderDetect_StateDirectory is the specific hole this work opened with:
// aider creates ~/.aider on first run but no ~/.aider.conf.yml, so a detector
// keyed on the config file alone misses a plainly-installed aider.
func TestAiderDetect_StateDirectory(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	testenv.ClearPath(t)
	if err := os.MkdirAll(filepath.Join(home, ".aider"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !(&Aider{}).Detect() {
		t.Error("~/.aider exists but aider was not detected")
	}
}

// TestAiderDetect_ConfigFile keeps the original file signal working; os.Stat
// accepts files, so a config file is a valid candidate alongside directories.
func TestAiderDetect_ConfigFile(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	testenv.ClearPath(t)
	if err := os.WriteFile(filepath.Join(home, ".aider.conf.yml"), []byte("read: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !(&Aider{}).Detect() {
		t.Error("~/.aider.conf.yml exists but aider was not detected")
	}
}

// TestDetectSignals_DescribesBothSignals covers the "not found" message: it must
// tell the user that installing the tool is sufficient, not just that a
// directory is missing.
func TestDetectSignals_DescribesBothSignals(t *testing.T) {
	got := DetectSignals(&ClaudeCode{})
	if !strings.Contains(got, ".claude") || !strings.Contains(got, "claude on PATH") {
		t.Errorf("DetectSignals = %q, want it to name both the config dir and the binary", got)
	}
	if DetectSignals(&Warp{}) == "" {
		t.Error("DetectSignals(Warp) = \"\", want its config candidates")
	}
}
