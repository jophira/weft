package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/testenv"
)

// ── DefaultDir ────────────────────────────────────────────────────────────────

func TestDefaultDir_containsHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	dir, err := config.DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}
	want := filepath.Join(home, ".config", "weft")
	if dir != want {
		t.Errorf("DefaultDir = %q, want %q", dir, want)
	}
}

// ── Defaults ──────────────────────────────────────────────────────────────────

func TestDefaults_subdirsUnderWeft(t *testing.T) {
	c, err := config.Defaults()
	if err != nil {
		t.Fatalf("Defaults: %v", err)
	}
	dir, _ := config.DefaultDir()
	if c.SourcesDir != filepath.Join(dir, "sources") {
		t.Errorf("SourcesDir = %q, want %q", c.SourcesDir, filepath.Join(dir, "sources"))
	}
	if c.ProfilesDir != filepath.Join(dir, "profiles") {
		t.Errorf("ProfilesDir = %q, want %q", c.ProfilesDir, filepath.Join(dir, "profiles"))
	}
	if c.HooksDir != filepath.Join(dir, "hooks") {
		t.Errorf("HooksDir = %q, want %q", c.HooksDir, filepath.Join(dir, "hooks"))
	}
}

func TestDefaults_warnSize(t *testing.T) {
	c, err := config.Defaults()
	if err != nil {
		t.Fatalf("Defaults: %v", err)
	}
	if c.WarnInstructionSizeKB != 96 {
		t.Errorf("WarnInstructionSizeKB = %d, want 96", c.WarnInstructionSizeKB)
	}
}

func TestDefaults_activeProfileEmpty(t *testing.T) {
	c, err := config.Defaults()
	if err != nil {
		t.Fatalf("Defaults: %v", err)
	}
	if c.ActiveProfile != "" {
		t.Errorf("ActiveProfile = %q, want empty", c.ActiveProfile)
	}
}

// ── EnsureDirs ────────────────────────────────────────────────────────────────

func TestEnsureDirs_createsAllDirs(t *testing.T) {
	base := t.TempDir()
	c := &config.Config{
		SourcesDir:  filepath.Join(base, "sources"),
		ProfilesDir: filepath.Join(base, "profiles"),
		HooksDir:    filepath.Join(base, "hooks"),
	}
	if err := config.EnsureDirs(c); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, d := range []string{c.SourcesDir, c.ProfilesDir, c.HooksDir} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("directory not created: %s: %v", d, err)
		}
	}
}

func TestEnsureDirs_idempotent(t *testing.T) {
	base := t.TempDir()
	c := &config.Config{
		SourcesDir:  filepath.Join(base, "sources"),
		ProfilesDir: filepath.Join(base, "profiles"),
		HooksDir:    filepath.Join(base, "hooks"),
	}
	// Call twice — must not error.
	if err := config.EnsureDirs(c); err != nil {
		t.Fatalf("first EnsureDirs: %v", err)
	}
	if err := config.EnsureDirs(c); err != nil {
		t.Fatalf("second EnsureDirs: %v", err)
	}
}

// ── SetWarnInstructionSizeKB / SetActiveProfile ───────────────────────────────

// withHome runs f with $HOME redirected to a temp directory so that
// SetWarnInstructionSizeKB and SetActiveProfile write to an isolated path.
func withHome(t *testing.T, f func(home string)) {
	t.Helper()
	tmp := t.TempDir()
	testenv.SetHome(t, tmp)
	f(tmp)
}

func TestSetWarnInstructionSizeKB_roundtrip(t *testing.T) {
	withHome(t, func(_ string) {
		if err := config.SetWarnInstructionSizeKB(128); err != nil {
			t.Fatalf("SetWarnInstructionSizeKB: %v", err)
		}
		// A second call with a different value should overwrite cleanly.
		if err := config.SetWarnInstructionSizeKB(64); err != nil {
			t.Fatalf("SetWarnInstructionSizeKB (2nd): %v", err)
		}
	})
}

func TestSetActiveProfile_roundtrip(t *testing.T) {
	withHome(t, func(_ string) {
		if err := config.SetActiveProfile("work"); err != nil {
			t.Fatalf("SetActiveProfile: %v", err)
		}
		// Overwrite with a different name.
		if err := config.SetActiveProfile("personal"); err != nil {
			t.Fatalf("SetActiveProfile (2nd): %v", err)
		}
	})
}

func TestSetActiveProfile_preservesOtherKeys(t *testing.T) {
	withHome(t, func(home string) {
		// Write warn size first, then set active profile.
		if err := config.SetWarnInstructionSizeKB(48); err != nil {
			t.Fatalf("SetWarnInstructionSizeKB: %v", err)
		}
		if err := config.SetActiveProfile("hybrid"); err != nil {
			t.Fatalf("SetActiveProfile: %v", err)
		}
		// The config file must still contain the warn_instruction_size_kb key.
		cfgPath := filepath.Join(home, ".config", "weft", "config.yaml")
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("reading config.yaml: %v", err)
		}
		content := string(data)
		if !containsSubstr(content, "warn_instruction_size_kb") {
			t.Errorf("config.yaml lost warn_instruction_size_kb after SetActiveProfile")
		}
		if !containsSubstr(content, "hybrid") {
			t.Errorf("config.yaml missing active_profile value after SetActiveProfile")
		}
	})
}

// TestSetActiveProfile_corruptYAML verifies that setKey returns an error when
// config.yaml exists but contains invalid YAML, and does NOT overwrite the file
// (data-loss protection for issue #87).
func TestSetActiveProfile_corruptYAML(t *testing.T) {
	withHome(t, func(home string) {
		cfgDir := filepath.Join(home, ".config", "weft")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		cfgPath := filepath.Join(cfgDir, "config.yaml")
		corrupt := []byte(":\tinvalid: [yaml: content\n")
		if err := os.WriteFile(cfgPath, corrupt, 0o644); err != nil {
			t.Fatalf("writing corrupt config: %v", err)
		}

		err := config.SetActiveProfile("should-fail")
		if err == nil {
			t.Fatal("SetActiveProfile: expected error for corrupt YAML, got nil")
		}

		// The file must be unchanged — not overwritten with only the new key.
		got, readErr := os.ReadFile(cfgPath)
		if readErr != nil {
			t.Fatalf("reading config after failed SetActiveProfile: %v", readErr)
		}
		if string(got) != string(corrupt) {
			t.Errorf("config.yaml was overwritten despite corrupt-YAML error\ngot:  %q\nwant: %q", got, corrupt)
		}
	})
}

// ── FilePath / ReadActiveProfile ──────────────────────────────────────────────

func TestReadActiveProfile_roundtripsSetActiveProfile(t *testing.T) {
	withHome(t, func(_ string) {
		if err := config.SetActiveProfile("hybrid"); err != nil {
			t.Fatalf("SetActiveProfile: %v", err)
		}
		got, err := config.ReadActiveProfile()
		if err != nil {
			t.Fatalf("ReadActiveProfile: %v", err)
		}
		if got != "hybrid" {
			t.Errorf("ReadActiveProfile = %q, want %q", got, "hybrid")
		}
	})
}

func TestReadActiveProfile_missingFile_returnsEmpty(t *testing.T) {
	withHome(t, func(_ string) {
		got, err := config.ReadActiveProfile()
		if err != nil {
			t.Fatalf("ReadActiveProfile on missing file: unexpected error %v", err)
		}
		if got != "" {
			t.Errorf("ReadActiveProfile on missing file = %q, want \"\"", got)
		}
	})
}

func TestReadActiveProfile_corruptYAML_returnsError(t *testing.T) {
	withHome(t, func(home string) {
		cfgDir := filepath.Join(home, ".config", "weft")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(":\tinvalid: [yaml\n"), 0o644); err != nil {
			t.Fatalf("writing corrupt config: %v", err)
		}
		if _, err := config.ReadActiveProfile(); err == nil {
			t.Fatal("ReadActiveProfile: expected error for corrupt YAML, got nil")
		}
	})
}

func TestFilePath_pointsAtConfigYAML(t *testing.T) {
	withHome(t, func(home string) {
		got, err := config.FilePath()
		if err != nil {
			t.Fatalf("FilePath: %v", err)
		}
		want := filepath.Join(home, ".config", "weft", "config.yaml")
		if got != want {
			t.Errorf("FilePath = %q, want %q", got, want)
		}
	})
}

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || sub == "" ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
