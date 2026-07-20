package harness

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/manifest"
)

// writeAdoptFile creates path (with parents) holding content.
func writeAdoptFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// claudeTarget builds a populated ~/.claude-shaped target root plus a source
// root, and returns both.
func claudeTarget(t *testing.T) (root, srcRoot, cfgDir string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "claude")
	srcRoot = filepath.Join(base, "source")
	cfgDir = filepath.Join(base, "cfg")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	return root, srcRoot, cfgDir
}

func layoutFor(root string) SourceLayout {
	return SourceLayout{Name: "personal", Root: root, Commands: "commands/", Agents: "agents/", Skills: "skills/"}
}

func TestScan_findsUnownedAndIgnoresOwned(t *testing.T) {
	root, _, _ := claudeTarget(t)
	writeAdoptFile(t, filepath.Join(root, "agents", "reviewer.md"), "# reviewer\n")
	writeAdoptFile(t, filepath.Join(root, "commands", "managed.md"), "# managed\n")
	writeAdoptFile(t, filepath.Join(root, "skills", "graphify", "SKILL.md"), "# skill\n")
	// Noise that must never surface.
	writeAdoptFile(t, filepath.Join(root, "CLAUDE.md"), "# instructions\n")
	writeAdoptFile(t, filepath.Join(root, "todos", "note.md"), "# todo\n")
	writeAdoptFile(t, filepath.Join(root, ".git", "commands", "hook.md"), "# vcs\n")
	writeAdoptFile(t, filepath.Join(root, "commands", "helper.sh"), "#!/bin/sh\n")

	got, err := Scan([]ScanTarget{{
		Harness: "claude-code",
		Root:    root,
		Owned:   map[string]string{filepath.Join("commands", "managed.md"): "sha256:whatever"},
		H:       &ClaudeCode{},
	}})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	want := map[string]Class{
		filepath.Join("agents", "reviewer.md"):          ClassAgents,
		filepath.Join("skills", "graphify", "SKILL.md"): ClassSkills,
	}
	if len(got) != len(want) {
		t.Fatalf("Scan returned %d candidate(s), want %d: %+v", len(got), len(want), got)
	}
	for _, c := range got {
		wantClass, ok := want[c.Rel]
		if !ok {
			t.Errorf("unexpected candidate %q", c.Rel)
			continue
		}
		if c.Class != wantClass {
			t.Errorf("candidate %q class = %q, want %q", c.Rel, c.Class, wantClass)
		}
	}
}

// Codex keeps commands in prompts/, so the reverse mapping must come from the
// harness's own declaration rather than weft's staged layout.
func TestScan_usesHarnessNativeDirs(t *testing.T) {
	root, _, _ := claudeTarget(t)
	writeAdoptFile(t, filepath.Join(root, "prompts", "review.md"), "# review\n")
	writeAdoptFile(t, filepath.Join(root, "commands", "review.md"), "# stray\n")

	got, err := Scan([]ScanTarget{{Harness: "codex", Root: root, H: &Codex{}}})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 || got[0].Rel != filepath.Join("prompts", "review.md") || got[0].Class != ClassCommands {
		t.Fatalf("Scan = %+v, want one commands candidate at prompts/review.md", got)
	}
}

func TestAdopt_copiesToClassCorrectPathAndRecordsOwnership(t *testing.T) {
	root, srcRoot, cfgDir := claudeTarget(t)
	rel := filepath.Join("agents", "reviewer.md")
	writeAdoptFile(t, filepath.Join(root, rel), "# reviewer\n")

	req := AdoptRequest{
		Target:    ScanTarget{Harness: "claude-code", Root: root, H: &ClaudeCode{}, CfgDir: cfgDir},
		Rels:      []string{rel},
		Layout:    layoutFor(srcRoot),
		Confirmed: true,
	}
	entries, err := Adopt(req)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Adopt returned %d entries, want 1", len(entries))
	}
	dst := filepath.Join(srcRoot, "agents", "reviewer.md")
	if got, readErr := os.ReadFile(dst); readErr != nil || string(got) != "# reviewer\n" {
		t.Fatalf("adopted content at %s = %q, err %v", dst, got, readErr)
	}
	m, err := manifest.Load(cfgDir, "claude-code")
	if err != nil {
		t.Fatalf("manifest load: %v", err)
	}
	if m.Files[rel] != manifest.HashBytes([]byte("# reviewer\n")) {
		t.Errorf("manifest does not record ownership of %q: %v", rel, m.Files)
	}
}

// Codex prompts/ maps to the source's commands/ dir — the class directory is
// translated, not preserved.
func TestAdopt_translatesNativeDirToSourceDir(t *testing.T) {
	root, srcRoot, cfgDir := claudeTarget(t)
	rel := filepath.Join("prompts", "review.md")
	writeAdoptFile(t, filepath.Join(root, rel), "# review\n")

	if _, err := Adopt(AdoptRequest{
		Target:    ScanTarget{Harness: "codex", Root: root, H: &Codex{}, CfgDir: cfgDir},
		Rels:      []string{rel},
		Layout:    layoutFor(srcRoot),
		Confirmed: true,
	}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(srcRoot, "commands", "review.md")); err != nil {
		t.Errorf("expected source commands/review.md: %v", err)
	}
}

func TestAdopt_requiresConfirmation(t *testing.T) {
	root, srcRoot, cfgDir := claudeTarget(t)
	rel := filepath.Join("agents", "reviewer.md")
	writeAdoptFile(t, filepath.Join(root, rel), "# reviewer\n")

	_, err := Adopt(AdoptRequest{
		Target: ScanTarget{Harness: "claude-code", Root: root, H: &ClaudeCode{}, CfgDir: cfgDir},
		Rels:   []string{rel},
		Layout: layoutFor(srcRoot),
	})
	if !errors.Is(err, ErrConfirmRequired) {
		t.Fatalf("Adopt without confirmation err = %v, want ErrConfirmRequired", err)
	}
	if _, statErr := os.Stat(filepath.Join(srcRoot, "agents", "reviewer.md")); statErr == nil {
		t.Error("unconfirmed adoption wrote to the source")
	}
}

func TestPlanAdopt_rejects(t *testing.T) {
	root, srcRoot, _ := claudeTarget(t)
	writeAdoptFile(t, filepath.Join(root, "agents", "reviewer.md"), "# reviewer\n")
	writeAdoptFile(t, filepath.Join(root, "agents", "clash.md"), "# harness copy\n")
	writeAdoptFile(t, filepath.Join(srcRoot, "agents", "clash.md"), "# source copy\n")
	writeAdoptFile(t, filepath.Join(root, "agents", "leaky.md"), "export KEY=\"sk-ant-abcdef0123456789\"\n")
	writeAdoptFile(t, filepath.Join(root, "agents", "entropy.md"), "token: k7Qz3PmXf9Rb2LwTn8Vd4Yc6\n")
	writeAdoptFile(t, filepath.Join(root, "notes", "scratch.md"), "# not a class\n")
	writeAdoptFile(t, filepath.Join(root, "agents", "script.sh"), "#!/bin/sh\n")
	writeAdoptFile(t, filepath.Join(root, ".git", "agents", "vcs.md"), "# vcs\n")

	owned := map[string]string{filepath.Join("agents", "reviewer.md"): "sha256:x"}

	tests := []struct {
		name    string
		rel     string
		owned   map[string]string
		force   bool
		wantErr string
	}{
		{"already owned", filepath.Join("agents", "reviewer.md"), owned, false, "already managed by weft"},
		{"destination exists", filepath.Join("agents", "clash.md"), nil, false, "--force"},
		{"vendor-prefixed secret", filepath.Join("agents", "leaky.md"), nil, false, "literal credential"},
		{"high-entropy secret", filepath.Join("agents", "entropy.md"), nil, false, "literal credential"},
		{"unadoptable class", filepath.Join("notes", "scratch.md"), nil, false, "no adoptable class"},
		{"non-markdown", filepath.Join("agents", "script.sh"), nil, false, "not adoptable"},
		{"inside .git", filepath.Join(".git", "agents", "vcs.md"), nil, false, "no adoptable class"},
		{"escapes root", filepath.Join("..", "outside.md"), nil, false, "escapes the harness root"},
		{"missing file", filepath.Join("agents", "ghost.md"), nil, false, "reading"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PlanAdopt(AdoptRequest{
				Target: ScanTarget{Harness: "claude-code", Root: root, Owned: tt.owned, H: &ClaudeCode{}},
				Rels:   []string{tt.rel},
				Layout: layoutFor(srcRoot),
				Force:  tt.force,
			})
			if err == nil {
				t.Fatalf("PlanAdopt(%s) succeeded, want error containing %q", tt.rel, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("PlanAdopt(%s) err = %q, want it to contain %q", tt.rel, err, tt.wantErr)
			}
		})
	}
}

func TestPlanAdopt_forceAllowsExistingDestination(t *testing.T) {
	root, srcRoot, _ := claudeTarget(t)
	rel := filepath.Join("agents", "clash.md")
	writeAdoptFile(t, filepath.Join(root, rel), "# harness copy\n")
	writeAdoptFile(t, filepath.Join(srcRoot, rel), "# source copy\n")

	entries, err := PlanAdopt(AdoptRequest{
		Target: ScanTarget{Harness: "claude-code", Root: root, H: &ClaudeCode{}},
		Rels:   []string{rel},
		Layout: layoutFor(srcRoot),
		Force:  true,
	})
	if err != nil {
		t.Fatalf("PlanAdopt with --force: %v", err)
	}
	if len(entries) != 1 || !entries[0].Overwrite {
		t.Fatalf("entries = %+v, want one flagged as overwrite", entries)
	}
}

func TestCheckNoSecrets_allowsOrdinaryProse(t *testing.T) {
	// Regression guard for the false-positive direction: a guard that fires on
	// normal rule text is a guard users learn to route around.
	body := "# Reviewer\n\nRead /home/philip/weft/sources/work-tech/common/code-review.md " +
		"and report findings. Use `${env:GITHUB_TOKEN}` for authentication.\n"
	if err := checkNoSecrets("agents/reviewer.md", []byte(body)); err != nil {
		t.Errorf("checkNoSecrets flagged ordinary prose: %v", err)
	}
}
