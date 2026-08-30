package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/testenv"
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
	tmp := t.TempDir()
	testenv.SetHome(t, tmp)

	result, err := loadConfigHarnesses()
	if err != nil {
		t.Fatalf("loadConfigHarnesses (missing file): unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("loadConfigHarnesses (missing file): expected nil, got %v", result)
	}
}

func TestLoadConfigHarnesses_withEntries(t *testing.T) {
	tmp := t.TempDir()
	testenv.SetHome(t, tmp)

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

// TestLoadConfigHarnesses_detectBinaryForms covers both spellings of
// detect_binary. The scalar form is what every harnesses.yaml written before the
// field was widened uses, and it must keep parsing unchanged.
func TestLoadConfigHarnesses_detectBinaryForms(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  []string
	}{
		{"scalar", "detect_binary: mytool", []string{"mytool"}},
		{"sequence", "detect_binary: [mytool, othername]", []string{"mytool", "othername"}},
		{"absent", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			testenv.SetHome(t, tmp)
			cfgDir := filepath.Join(tmp, ".config", "weft")
			if err := os.MkdirAll(cfgDir, 0o755); err != nil {
				t.Fatal(err)
			}
			doc := "harnesses:\n  - name: mytool\n    config_dir: .mytool\n"
			if tc.field != "" {
				doc += "    " + tc.field + "\n"
			}
			if err := os.WriteFile(filepath.Join(cfgDir, "harnesses.yaml"), []byte(doc), 0o644); err != nil {
				t.Fatal(err)
			}

			result, err := loadConfigHarnesses()
			if err != nil {
				t.Fatalf("loadConfigHarnesses: %v", err)
			}
			if len(result) != 1 {
				t.Fatalf("loadConfigHarnesses: len=%d, want 1", len(result))
			}
			got := result[0].H.(*GenericHarness).detectBinaries
			if len(got) != len(tc.want) {
				t.Fatalf("detectBinaries = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("detectBinaries[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestLoadConfigHarnesses_corruptYAML(t *testing.T) {
	tmp := t.TempDir()
	testenv.SetHome(t, tmp)

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
	g := &GenericHarness{detection: resolved("/home/user/.mytool"), name: "mytool"}
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

// ── entry validation (#279) ──────────────────────────────────────────────────

// writeHarnessesYAML points $HOME at a temp dir and puts doc in its
// harnesses.yaml, so loadConfigHarnesses reads exactly that.
func writeHarnessesYAML(t *testing.T, doc string) {
	t.Helper()
	tmp := t.TempDir()
	testenv.SetHome(t, tmp)
	cfgDir := filepath.Join(tmp, ".config", "weft")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "harnesses.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// config_dir and detect_path are joined onto $HOME, so anything that escapes it
// turns a user-defined harness into a write primitive pointed wherever it likes.
func TestLoadConfigHarnesses_rejectsEscapingPaths(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"config_dir parent", "harnesses:\n  - name: bad\n    config_dir: ../../etc\n"},
		{"config_dir absolute", "harnesses:\n  - name: bad\n    config_dir: /etc\n"},
		{"config_dir sneaky", "harnesses:\n  - name: bad\n    config_dir: a/../../../etc\n"},
		{"config_dir home itself", "harnesses:\n  - name: bad\n    config_dir: .\n"},
		{"config_dir drive", `harnesses:` + "\n  - name: bad\n    config_dir: \"C:evil\"\n"},
		{"detect_path parent", "harnesses:\n  - name: bad\n    config_dir: .ok\n    detect_path: ../../etc\n"},
		{"detect_path absolute", "harnesses:\n  - name: bad\n    config_dir: .ok\n    detect_path: /etc\n"},
		{"no name", "harnesses:\n  - config_dir: .mytool\n"},
		{"no paths", "harnesses:\n  - name: bad\n"},
		{"duplicate names", "harnesses:\n  - name: dup\n    config_dir: .a\n  - name: dup\n    config_dir: .b\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeHarnessesYAML(t, tc.doc)
			got, err := loadConfigHarnesses()
			if err == nil {
				t.Fatalf("loadConfigHarnesses accepted the entry, returning %d harnesses", len(got))
			}
			if got != nil {
				t.Errorf("a rejected load must return no harnesses, got %d", len(got))
			}
		})
	}
}

// The ordinary shapes must keep loading — validation that rejects real configs
// is worse than the hole it closes.
func TestLoadConfigHarnesses_acceptsOrdinaryPaths(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"dot dir", "harnesses:\n  - name: mytool\n    config_dir: .mytool\n"},
		{"nested", "harnesses:\n  - name: mytool\n    config_dir: .config/mytool\n"},
		{"dot segment inside", "harnesses:\n  - name: mytool\n    config_dir: .config/./mytool\n"},
		{"up then back", "harnesses:\n  - name: mytool\n    config_dir: .config/x/../mytool\n"},
		{"detect only", "harnesses:\n  - name: mytool\n    detect_path: .mytool\n"},
		{"both differing", "harnesses:\n  - name: mytool\n    config_dir: .mytool\n    detect_path: .config/mytool\n"},
		{"two harnesses", "harnesses:\n  - name: one\n    config_dir: .one\n  - name: two\n    config_dir: .two\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeHarnessesYAML(t, tc.doc)
			if _, err := loadConfigHarnesses(); err != nil {
				t.Fatalf("loadConfigHarnesses rejected an ordinary config: %v", err)
			}
		})
	}
}
