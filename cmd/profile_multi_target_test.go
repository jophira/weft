package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/testenv"
)

// ── resolveApplyTargets ───────────────────────────────────────────────────────

func TestResolveApplyTargets_ConfiguredTargets(t *testing.T) {
	p := &profile.Profile{Targets: []string{"claude-code", "codex"}}
	got := resolveApplyTargets(p, true)
	want := []string{"claude-code", "codex"}
	if len(got) != len(want) {
		t.Fatalf("resolveApplyTargets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("resolveApplyTargets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveApplyTargets_LegacyActiveTarget(t *testing.T) {
	p := &profile.Profile{ActiveTarget: "claude-code"}
	got := resolveApplyTargets(p, true)
	if len(got) != 1 || got[0] != "claude-code" {
		t.Errorf("resolveApplyTargets = %v, want [claude-code]", got)
	}
}

func TestResolveApplyTargets_AutoDetect_ClaudeCode(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &profile.Profile{}
	got := resolveApplyTargets(p, true)
	found := false
	for _, name := range got {
		if name == "claude-code" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected claude-code to be auto-detected, got %v", got)
	}
}

func TestResolveApplyTargets_NoneDetected(t *testing.T) {
	home := t.TempDir() // empty home — no harnesses installed
	testenv.SetHome(t, home)
	p := &profile.Profile{}
	got := resolveApplyTargets(p, true)
	if len(got) != 0 {
		t.Errorf("expected no targets, got %v", got)
	}
}

// ── mergeAndApply multi-target ────────────────────────────────────────────────

// TestMergeAndApply_MultiTarget verifies that mergeAndApply writes files to
// every target listed in the profile.
func TestMergeAndApply_MultiTarget(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	srcRoot := t.TempDir()
	writeFile(t, filepath.Join(srcRoot, "CLAUDE.md"), "# shared rules")
	cfgDir := t.TempDir()

	p := &profile.Profile{
		Name:    "multi",
		Sources: []string{"test"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"claude-code", "codex"},
	}
	srcs := []source.Source{newSource("test", srcRoot)}

	if err := mergeAndApply(p, []string{srcRoot}, srcs, cfgDir, true); err != nil {
		t.Fatalf("mergeAndApply: %v", err)
	}

	// claude-code writes CLAUDE.md as-is
	claudeFile := filepath.Join(home, ".claude", "CLAUDE.md")
	if _, err := os.Stat(claudeFile); err != nil {
		t.Errorf("expected CLAUDE.md in ~/.claude, got: %v", err)
	}

	// codex renames CLAUDE.md → AGENTS.md
	codexFile := filepath.Join(home, ".codex", "AGENTS.md")
	if _, err := os.Stat(codexFile); err != nil {
		t.Errorf("expected AGENTS.md in ~/.codex, got: %v", err)
	}
}

// TestMergeAndApply_SingleTarget verifies the single-target path is unchanged.
func TestMergeAndApply_SingleTarget(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	srcRoot := t.TempDir()
	writeFile(t, filepath.Join(srcRoot, "CLAUDE.md"), "# rules")
	cfgDir := t.TempDir()

	p := &profile.Profile{
		Name:    "single",
		Sources: []string{"test"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"claude-code"},
	}
	srcs := []source.Source{newSource("test", srcRoot)}

	if err := mergeAndApply(p, []string{srcRoot}, srcs, cfgDir, true); err != nil {
		t.Fatalf("mergeAndApply: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, ".claude", "CLAUDE.md")); err != nil {
		t.Errorf("expected CLAUDE.md in ~/.claude: %v", err)
	}
	// codex dir should NOT exist
	if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
		t.Error("~/.codex should not have been created for single-target apply")
	}
}

// TestMergeAndApply_NoTargetsNoHarnesses returns without error when neither
// configured targets nor detectable harnesses exist.
func TestMergeAndApply_NoTargetsNoHarnesses(t *testing.T) {
	home := t.TempDir() // empty home — nothing installed
	testenv.SetHome(t, home)

	srcRoot := t.TempDir()
	writeFile(t, filepath.Join(srcRoot, "CLAUDE.md"), "# rules")
	cfgDir := t.TempDir()

	p := &profile.Profile{
		Name:    "empty",
		Sources: []string{"test"},
		Overlay: profile.OverlayCascade,
	}
	srcs := []source.Source{newSource("test", srcRoot)}

	if err := mergeAndApply(p, []string{srcRoot}, srcs, cfgDir, true); err != nil {
		t.Fatalf("mergeAndApply with no targets should not error: %v", err)
	}
}
