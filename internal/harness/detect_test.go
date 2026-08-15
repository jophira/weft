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

// TestDetect_SecondBinaryName is the reason detectSpec.binaries is a slice: a
// tool that ships its entry point under an alternative name offers no other
// signal on a fresh install, and the evidence must name the binary that matched
// rather than the first one tried.
func TestDetect_SecondBinaryName(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	fakeBinary(t, "othername")

	g := &GenericHarness{name: "mytool", detectBinaries: []string{"mytool", "othername"}}
	if !g.Detect() {
		t.Fatal("othername on PATH but Detect returned false")
	}
	via, detail := g.DetectedVia()
	if via != DetectBinary {
		t.Fatalf("via = %v, want DetectBinary", via)
	}
	if !strings.HasSuffix(detail, "othername") {
		t.Errorf("detail = %q, want the path to othername", detail)
	}
}

// TestDetect_BinaryOrderIsPriority pins that the first declared name wins when
// both are installed, so a harness can express which entry point it prefers.
func TestDetect_BinaryOrderIsPriority(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	dir := fakeBinaryDir(t, "first", "second")

	g := &GenericHarness{name: "mytool", detectBinaries: []string{"first", "second"}}
	if !g.Detect() {
		t.Fatal("both binaries on PATH but Detect returned false")
	}
	if _, detail := g.DetectedVia(); !strings.HasSuffix(detail, "first") {
		t.Errorf("detail = %q, want %s/first", detail, dir)
	}
}

// TestOpencodeDetect_DataRoot covers the reported miss: opencode creates its
// config root on first run, so a machine that has installed it but never run it
// is only visible through the XDG data root.
func TestOpencodeDetect_DataRoot(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	testenv.ClearPath(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}

	h := opencodeHarness(t)
	if !h.Detect() {
		t.Fatal("~/.local/share/opencode exists but opencode was not detected")
	}
	via, detail := h.(DetectReporter).DetectedVia()
	if via != DetectConfigDir {
		t.Fatalf("via = %v, want DetectConfigDir", via)
	}
	if !strings.Contains(detail, ".local/share/opencode") {
		t.Errorf("detail = %q, want it to name the data root", detail)
	}
}

// TestOpencodeDetect_ConfigRootWins keeps the write target ahead of the data
// root: weft projects into the config directory, so that is the root to report
// when both exist.
func TestOpencodeDetect_ConfigRootWins(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	testenv.ClearPath(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	for _, d := range []string{
		filepath.Join(home, ".config", "opencode"),
		filepath.Join(home, ".local", "share", "opencode"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	h := opencodeHarness(t)
	if !h.Detect() {
		t.Fatal("both roots exist but opencode was not detected")
	}
	_, detail := h.(DetectReporter).DetectedVia()
	if !strings.Contains(detail, ".config/opencode") {
		t.Errorf("detail = %q, want the config root to take priority", detail)
	}
}

// fakeBinaryDir puts several executables on one temp PATH and returns the dir.
func fakeBinaryDir(t *testing.T, names ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("binary detection uses PATHEXT on Windows; covered on Unix")
	}
	dir := t.TempDir()
	for _, n := range names {
		//nolint:gosec // test fixture must be executable
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

// opencodeHarness returns the built-in opencode adapter, so the tests assert the
// shipped detection table rather than a copy of it.
func opencodeHarness(t *testing.T) Harness {
	t.Helper()
	for _, k := range builtins() {
		if k.H.Name() == "opencode" {
			return k.H
		}
	}
	t.Fatal("opencode is not in the built-in harness list")
	return nil
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

// TestDetectedSignal_NamesTheKindOfEvidence covers the detect report's signal
// column: a config hit and a binary hit must stay distinguishable, since only
// the first names a root weft will write to.
func TestDetectedSignal_NamesTheKindOfEvidence(t *testing.T) {
	t.Run("config dir", func(t *testing.T) {
		home := t.TempDir()
		testenv.SetHome(t, home)
		testenv.ClearPath(t)
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		c := &ClaudeCode{}
		if !c.Detect() {
			t.Fatal("~/.claude exists but Detect returned false")
		}
		got := DetectedSignal(c)
		if !strings.HasPrefix(got, "config ") || !strings.Contains(got, ".claude") {
			t.Errorf("DetectedSignal = %q, want a config hit naming ~/.claude", got)
		}
	})

	t.Run("binary", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir()) // empty home — only the binary can match
		fakeBinary(t, "claude")
		c := &ClaudeCode{}
		if !c.Detect() {
			t.Fatal("claude on PATH but Detect returned false")
		}
		got := DetectedSignal(c)
		if !strings.HasPrefix(got, "binary ") || !strings.HasSuffix(got, "claude") {
			t.Errorf("DetectedSignal = %q, want a binary hit naming the executable", got)
		}
	})

	t.Run("not found", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		testenv.ClearPath(t)
		c := &ClaudeCode{}
		c.Detect()
		if got := DetectedSignal(c); got != "" {
			t.Errorf("DetectedSignal = %q, want empty when nothing matched", got)
		}
	})
}

// TestDetectSignals_NamesEveryBinary keeps the "not found" message honest once a
// harness declares alternatives: naming only the first sends users looking for
// an entry point their install may not have.
func TestDetectSignals_NamesEveryBinary(t *testing.T) {
	g := &GenericHarness{name: "mytool", detectBinaries: []string{"mytool", "othername"}}
	got := DetectSignals(g)
	if !strings.Contains(got, "mytool") || !strings.Contains(got, "othername") {
		t.Errorf("DetectSignals = %q, want it to name both binaries", got)
	}
}
