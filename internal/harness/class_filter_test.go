package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// applyWithClasses runs a Claude Code apply over a staged tree containing one
// file per class, restricted to the given class set, and returns the log.
func applyWithClasses(t *testing.T, allowed map[Class]bool) (target, log string) {
	t.Helper()
	staged := t.TempDir()
	write(t, filepath.Join(staged, "commands", "review.md"), "cmd")
	write(t, filepath.Join(staged, "agents", "explorer.md"), "agent")
	write(t, filepath.Join(staged, "skills", "graphify", "SKILL.md"), "skill")

	root := t.TempDir()
	buf := &bytes.Buffer{}
	ctx := ApplyCtx{
		ProfileName:    "test",
		CfgDir:         t.TempDir(),
		Out:            buf,
		AllowedClasses: allowed,
	}
	h := &GenericHarness{name: "test-harness", root: root}
	if err := applyWithManifest(staged, root, h.name, ctx, nil, nil, h); err != nil {
		t.Fatal(err)
	}
	return root, buf.String()
}

func exists(t *testing.T, parts ...string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(parts...))
	return err == nil
}

// Nil means unrestricted — the behaviour of every profile written before
// harness_sync existed.
func TestApply_NilAllowedClassesProjectsEverything(t *testing.T) {
	root, _ := applyWithClasses(t, nil)

	for _, rel := range [][]string{
		{"commands", "review.md"},
		{"agents", "explorer.md"},
		{"skills", "graphify", "SKILL.md"},
	} {
		if !exists(t, append([]string{root}, rel...)...) {
			t.Errorf("%v should have been projected when unrestricted", rel)
		}
	}
}

func TestApply_RestrictedClassesWithholdTheRest(t *testing.T) {
	root, log := applyWithClasses(t, map[Class]bool{ClassCommands: true})

	if !exists(t, root, "commands", "review.md") {
		t.Error("commands is allowed and should have been projected")
	}
	if exists(t, root, "agents", "explorer.md") {
		t.Error("agents is not allowed and must not be written")
	}
	if exists(t, root, "skills", "graphify", "SKILL.md") {
		t.Error("skills is not allowed and must not be written")
	}

	// The user needs to see that their own config withheld these, not assume
	// the harness could not take them.
	if !bytes.Contains([]byte(log), []byte("excluded by harness_sync config")) {
		t.Errorf("log should attribute the exclusion to config:\n%s", log)
	}
}

// An explicitly empty class set is a deliberate "project nothing".
func TestApply_EmptyAllowedClassesProjectsNothing(t *testing.T) {
	root, _ := applyWithClasses(t, map[Class]bool{})

	for _, dir := range []string{"commands", "agents", "skills"} {
		if exists(t, root, dir) {
			t.Errorf("%s should not exist when no class is allowed", dir)
		}
	}
}

// Withholding a class must not let it reappear as an advertised index entry —
// that would route around the config meant to suppress it.
func TestProjectInstruction_ExcludedClassIsNotAdvertised(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "agents", "explorer.md"),
		"---\nname: explorer\ndescription: search agent\n---\nbody")

	home := t.TempDir()
	instrPath := filepath.Join(home, "AGENTS.md")
	h := &stubInstructed{spec: InstructionSpec{Path: instrPath, Strategy: StrategyInline}}

	ctx := ApplyCtx{
		ProfileName:    "test",
		CfgDir:         t.TempDir(),
		AllowedClasses: map[Class]bool{ClassInstructions: true}, // agents withheld
	}
	if err := ProjectInstruction(h, staged, []SourceInstruction{{Name: "s", Content: "rules"}}, ctx); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(instrPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("explorer")) {
		t.Errorf("withheld class must not be advertised:\n%s", got)
	}
}

// Withholding instructions must skip the instruction file entirely rather than
// writing an empty managed block over the user's file.
func TestProjectInstruction_ExcludedInstructionsIsNoOp(t *testing.T) {
	home := t.TempDir()
	instrPath := filepath.Join(home, "AGENTS.md")
	h := &stubInstructed{spec: InstructionSpec{Path: instrPath, Strategy: StrategyInline}}

	ctx := ApplyCtx{
		ProfileName:    "test",
		CfgDir:         t.TempDir(),
		AllowedClasses: map[Class]bool{ClassCommands: true},
	}
	if err := ProjectInstruction(h, "", []SourceInstruction{{Name: "s", Content: "rules"}}, ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(instrPath); err == nil {
		t.Error("instruction file must not be written when the class is withheld")
	}
}
