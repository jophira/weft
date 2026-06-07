package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/locate"
)

// ── entryCandidates ───────────────────────────────────────────────────────────

func TestEntryCandidates_configDir(t *testing.T) {
	e := harnessEntry{Name: "mytool", ConfigDir: ".mytool"}
	candidates := entryCandidates(e)
	if len(candidates) != 1 {
		t.Fatalf("entryCandidates: len=%d, want 1", len(candidates))
	}
	home, _ := os.UserHomeDir()
	got := candidates[0].Path(home, "")
	want := filepath.Join(home, ".mytool")
	if got != want {
		t.Errorf("candidates[0].Path = %q, want %q", got, want)
	}
}

func TestEntryCandidates_separateDetectPath(t *testing.T) {
	e := harnessEntry{Name: "mytool", ConfigDir: ".mytool-config", DetectPath: ".mytool-detect"}
	candidates := entryCandidates(e)
	if len(candidates) != 2 {
		t.Fatalf("entryCandidates: len=%d, want 2 (separate detect and config paths)", len(candidates))
	}
}

func TestEntryCandidates_sameDetectAndConfigPath(t *testing.T) {
	e := harnessEntry{Name: "mytool", ConfigDir: ".mytool", DetectPath: ".mytool"}
	candidates := entryCandidates(e)
	if len(candidates) != 1 {
		t.Fatalf("entryCandidates: len=%d, want 1 (same path, deduped)", len(candidates))
	}
}

func TestEntryCandidates_empty(t *testing.T) {
	e := harnessEntry{Name: "empty"}
	candidates := entryCandidates(e)
	if len(candidates) != 0 {
		t.Errorf("entryCandidates(empty): len=%d, want 0", len(candidates))
	}
}

// ── loadConfigHarnesses ───────────────────────────────────────────────────────

func TestLoadConfigHarnesses_missingFile(t *testing.T) {
	orig, hadOrig := os.LookupEnv("HOME")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	defer func() {
		if hadOrig {
			os.Setenv("HOME", orig) //nolint:errcheck,gosec // restoring env in test teardown is best-effort
		} else {
			os.Unsetenv("HOME") //nolint:errcheck,gosec // restoring env in test teardown is best-effort
		}
	}()

	result, err := loadConfigHarnesses()
	if err != nil {
		t.Fatalf("loadConfigHarnesses (missing file): unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("loadConfigHarnesses (missing file): expected nil, got %v", result)
	}
}

func TestLoadConfigHarnesses_withEntries(t *testing.T) {
	orig, hadOrig := os.LookupEnv("HOME")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	defer func() {
		if hadOrig {
			os.Setenv("HOME", orig) //nolint:errcheck,gosec // restoring env in test teardown is best-effort
		} else {
			os.Unsetenv("HOME") //nolint:errcheck,gosec // restoring env in test teardown is best-effort
		}
	}()

	cfgDir := filepath.Join(tmp, ".config", "weft")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `harnesses:
  - name: mytool
    config_dir: .mytool
`
	if err := os.WriteFile(filepath.Join(cfgDir, "harnesses.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := loadConfigHarnesses()
	if err != nil {
		t.Fatalf("loadConfigHarnesses: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("loadConfigHarnesses: len=%d, want 1", len(result))
	}
	if result[0].H.Name() != "mytool" {
		t.Errorf("loadConfigHarnesses: Name = %q, want mytool", result[0].H.Name())
	}
}

func TestLoadConfigHarnesses_corruptYAML(t *testing.T) {
	orig, hadOrig := os.LookupEnv("HOME")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	defer func() {
		if hadOrig {
			os.Setenv("HOME", orig) //nolint:errcheck,gosec // restoring env in test teardown is best-effort
		} else {
			os.Unsetenv("HOME") //nolint:errcheck,gosec // restoring env in test teardown is best-effort
		}
	}()

	cfgDir := filepath.Join(tmp, ".config", "weft")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "harnesses.yaml"), []byte(":\tinvalid\tyaml:"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadConfigHarnesses()
	if err == nil {
		t.Error("loadConfigHarnesses with corrupt YAML: expected error, got nil")
	}
}

// ── GenericHarness ────────────────────────────────────────────────────────────

func TestGenericHarness_Name(t *testing.T) {
	g := &GenericHarness{name: "mytool"}
	if g.Name() != "mytool" {
		t.Errorf("Name() = %q, want mytool", g.Name())
	}
}

func TestGenericHarness_ConfigPath_noRoot(t *testing.T) {
	g := &GenericHarness{name: "mytool", candidates: nil}
	// No root, no candidates → should return empty or display string.
	_ = g.ConfigPath() // must not panic
}

func TestGenericHarness_ConfigPath_withRoot(t *testing.T) {
	g := &GenericHarness{name: "mytool", root: "/home/user/.mytool"}
	got := g.ConfigPath()
	if got == "" {
		t.Error("ConfigPath with root set returned empty string")
	}
}

func TestGenericHarness_Detect_existingDir(t *testing.T) {
	dir := t.TempDir()
	g := &GenericHarness{
		name: "mytool",
		candidates: []locate.Candidate{
			{Path: func(_, _ string) string { return dir }},
		},
	}
	if !g.Detect() {
		t.Error("Detect with existing candidate dir: expected true")
	}
	if g.root != dir {
		t.Errorf("Detect: root = %q, want %q", g.root, dir)
	}
}

func TestGenericHarness_Detect_noCandidates(t *testing.T) {
	g := &GenericHarness{name: "mytool", candidates: nil}
	if g.Detect() {
		t.Error("Detect with no candidates: expected false")
	}
}
