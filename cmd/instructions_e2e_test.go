package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/runstate"
	"github.com/jophira/weft/internal/source"
)

// buildLayeredSources writes three priority-ordered source trees exercising
// flat instructions, a projects placeholder + project-rules tree, commands, and
// skills. Returns the sources in low→high priority order (as mergeAndApply
// expects them pre-sorted).
func buildLayeredSources(t *testing.T) []source.Source {
	t.Helper()

	personal := t.TempDir()
	writeFile(t, filepath.Join(personal, "CLAUDE.md"), "# personal rules\n\n<!-- weft:projects -->\n")
	writeFile(t, filepath.Join(personal, "project-rules", "myproj", "myproj.md"), "# myproj rules")
	writeFile(t, filepath.Join(personal, "commands", "hello.md"), "say hello")

	team := t.TempDir()
	writeFile(t, filepath.Join(team, "CLAUDE.md"), "# team rules")
	writeFile(t, filepath.Join(team, "skills", "lint", "SKILL.md"), "# lint skill")

	company := t.TempDir()
	writeFile(t, filepath.Join(company, "CLAUDE.md"), "# company rules")

	ds := source.DefaultStructure()
	return []source.Source{
		{Name: "personal", Root: personal, Priority: 10, Structure: ds},
		{Name: "team", Root: team, Priority: 20, Structure: ds},
		{Name: "company", Root: company, Priority: 30, Structure: ds},
	}
}

func rootsOf(srcs []source.Source) []string {
	roots := make([]string, len(srcs))
	for i, s := range srcs {
		roots[i] = s.Root
	}
	return roots
}

func TestMergeAndApply_TierA_importBlockAndCopies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	srcs := buildLayeredSources(t)
	p := &profile.Profile{
		Name:    "layered",
		Sources: []string{"personal", "team", "company"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"claude-code"},
	}

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, true); err != nil {
		t.Fatalf("mergeAndApply: %v", err)
	}

	// 1. weft-owned per-source copies exist in priority order.
	instrDir := filepath.Join(cfgDir, "profiles", "layered", "instructions")
	for _, name := range []string{"00-personal.md", "01-team.md", "02-company.md"} {
		if _, err := os.Stat(filepath.Join(instrDir, name)); err != nil {
			t.Errorf("expected instruction copy %s: %v", name, err)
		}
	}

	// 2. The personal copy expanded its projects placeholder with its own files.
	personalCopy := readFile(t, filepath.Join(instrDir, "00-personal.md"))
	if !strings.Contains(personalCopy, "weft:projects:begin") || !strings.Contains(personalCopy, "myproj.md") {
		t.Errorf("projects placeholder not expanded in personal copy:\n%s", personalCopy)
	}

	// 3. ~/.claude/CLAUDE.md is a thin import block in priority order, no bodies.
	claude := readFile(t, filepath.Join(home, ".claude", "CLAUDE.md"))
	if !strings.Contains(claude, "weft:begin") {
		t.Fatalf("no managed block in ~/.claude/CLAUDE.md:\n%s", claude)
	}
	i := strings.Index(claude, "00-personal.md")
	j := strings.Index(claude, "01-team.md")
	k := strings.Index(claude, "02-company.md")
	if i < 0 || j < 0 || k < 0 || i >= j || j >= k {
		t.Errorf("imports missing or out of priority order (i=%d j=%d k=%d):\n%s", i, j, k, claude)
	}
	if strings.Contains(claude, "# company rules") {
		t.Errorf("Tier A root file should not inline source bodies:\n%s", claude)
	}

	// 4. Sidecars copied into the harness dir.
	for _, rel := range []string{
		filepath.Join("commands", "hello.md"),
		filepath.Join("skills", "lint", "SKILL.md"),
	} {
		if _, err := os.Stat(filepath.Join(home, ".claude", rel)); err != nil {
			t.Errorf("expected sidecar %s copied to ~/.claude: %v", rel, err)
		}
	}
}

func TestMergeAndApply_TierA_copiesAreReadOnlyWithGeneratedHeader(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	srcs := buildLayeredSources(t)
	p := &profile.Profile{
		Name:    "layered",
		Sources: []string{"personal", "team", "company"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"claude-code"},
	}

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, true); err != nil {
		t.Fatalf("mergeAndApply: %v", err)
	}

	instrDir := filepath.Join(cfgDir, "profiles", "layered", "instructions")
	personalCopyPath := filepath.Join(instrDir, "00-personal.md")

	info, err := os.Stat(personalCopyPath)
	if err != nil {
		t.Fatalf("stat instruction copy: %v", err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Errorf("instruction copy should be read-only, got mode %v", info.Mode().Perm())
	}

	content := readFile(t, personalCopyPath)
	if !strings.HasPrefix(content, `<!-- weft: generated from source "personal"`) {
		t.Errorf("instruction copy missing generated-file header:\n%s", content)
	}

	// Re-apply must still rewrite the (now read-only) copies cleanly.
	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, true); err != nil {
		t.Fatalf("re-apply mergeAndApply: %v", err)
	}
}

func TestMergeAndApply_TierB_inlineAttributedContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	srcs := buildLayeredSources(t)
	p := &profile.Profile{
		Name:    "layered",
		Sources: []string{"personal", "team", "company"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"codex"},
	}

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, true); err != nil {
		t.Fatalf("mergeAndApply: %v", err)
	}

	agents := readFile(t, filepath.Join(home, ".codex", "AGENTS.md"))
	for _, frag := range []string{
		"weft:begin",
		`weft:source:begin name="personal"`,
		"# personal rules",
		`weft:source:begin name="company"`,
		"# company rules",
	} {
		if !strings.Contains(agents, frag) {
			t.Errorf("Tier B AGENTS.md missing %q:\n%s", frag, agents)
		}
	}
	// Priority order preserved in the inline content.
	if strings.Index(agents, "# personal rules") > strings.Index(agents, "# company rules") {
		t.Error("inline content out of priority order (personal should precede company)")
	}
}

func TestInstructionWriteBack_TierBEditFlowsToSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	srcs := buildLayeredSources(t)
	p := &profile.Profile{
		Name:    "layered",
		Sources: []string{"personal", "team", "company"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"codex"},
	}

	// Initial apply writes the Tier B inline block.
	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, false); err != nil {
		t.Fatalf("initial mergeAndApply: %v", err)
	}

	// Simulate the user editing the inlined personal section in the harness file.
	agentsPath := filepath.Join(home, ".codex", "AGENTS.md")
	edited := strings.Replace(readFile(t, agentsPath), "# personal rules", "# EDITED personal rules", 1)
	if !strings.Contains(edited, "# EDITED personal rules") {
		t.Fatal("test setup: edit did not apply")
	}
	writeFile(t, agentsPath, edited)

	// Re-apply: write-back must carry the edit into the personal source.
	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, false); err != nil {
		t.Fatalf("re-apply mergeAndApply: %v", err)
	}

	personalSrc := readFile(t, filepath.Join(srcs[0].Root, "CLAUDE.md"))
	if !strings.Contains(personalSrc, "# EDITED personal rules") {
		t.Errorf("edit did not reach the personal source:\n%s", personalSrc)
	}
	// Generated projects block must be collapsed back to its placeholder in source.
	if !strings.Contains(personalSrc, "<!-- weft:projects -->") {
		t.Errorf("projects placeholder not restored on write-back:\n%s", personalSrc)
	}
	if strings.Contains(personalSrc, "weft:source:begin") {
		t.Errorf("attribution markers leaked into source:\n%s", personalSrc)
	}
}

// twoTierBTargets returns a profile targeting codex and windsurf, both Tier B,
// for exercising cross-harness instruction conflicts (#257).
func twoTierBTargets() *profile.Profile {
	return &profile.Profile{
		Name:    "layered",
		Sources: []string{"personal", "team", "company"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"codex", "windsurf"},
	}
}

func TestInstructionConflict_TwoHarnessesSameSection_HeldNotOverwritten(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	srcs := buildLayeredSources(t)
	p := twoTierBTargets()

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, false); err != nil {
		t.Fatalf("initial mergeAndApply: %v", err)
	}

	agentsPath := filepath.Join(home, ".codex", "AGENTS.md")
	rulesPath := filepath.Join(home, ".codeium", "windsurf", "global_rules.md")

	// Both harnesses edit the *same* source's section, without an apply in between.
	writeFile(t, agentsPath, strings.Replace(readFile(t, agentsPath), "# personal rules", "# personal rules\nbranch-naming rule", 1))
	writeFile(t, rulesPath, strings.Replace(readFile(t, rulesPath), "# personal rules", "# personal rules\ntest-naming rule", 1))

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, false); err != nil {
		t.Fatalf("re-apply mergeAndApply: %v", err)
	}

	// Source must not have been written from either side.
	personalSrc := readFile(t, filepath.Join(srcs[0].Root, "CLAUDE.md"))
	if strings.Contains(personalSrc, "branch-naming rule") || strings.Contains(personalSrc, "test-naming rule") {
		t.Errorf("conflicting edit reached the source — should have been held:\n%s", personalSrc)
	}

	// Each harness must still hold its own edit, not the other's and not the base.
	agentsAfter := readFile(t, agentsPath)
	if !strings.Contains(agentsAfter, "branch-naming rule") {
		t.Errorf("codex lost its own edit on a held conflict:\n%s", agentsAfter)
	}
	if strings.Contains(agentsAfter, "test-naming rule") {
		t.Errorf("codex was overwritten with windsurf's edit:\n%s", agentsAfter)
	}

	rulesAfter := readFile(t, rulesPath)
	if !strings.Contains(rulesAfter, "test-naming rule") {
		t.Errorf("windsurf lost its own edit on a held conflict:\n%s", rulesAfter)
	}
	if strings.Contains(rulesAfter, "branch-naming rule") {
		t.Errorf("windsurf was overwritten with codex's edit:\n%s", rulesAfter)
	}

	counts, err := runstate.ReadCounts(cfgDir)
	if err != nil {
		t.Fatalf("reading status counts: %v", err)
	}
	if counts.Conflicts < 1 {
		t.Errorf("expected the instruction conflict to be counted, got %d", counts.Conflicts)
	}
}

func TestInstructionConflict_OneDivergedHarness_IsOrdinaryWriteBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	srcs := buildLayeredSources(t)
	p := twoTierBTargets()

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, false); err != nil {
		t.Fatalf("initial mergeAndApply: %v", err)
	}

	agentsPath := filepath.Join(home, ".codex", "AGENTS.md")
	writeFile(t, agentsPath, strings.Replace(readFile(t, agentsPath), "# personal rules", "# EDITED personal rules", 1))

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, false); err != nil {
		t.Fatalf("re-apply mergeAndApply: %v", err)
	}

	personalSrc := readFile(t, filepath.Join(srcs[0].Root, "CLAUDE.md"))
	if !strings.Contains(personalSrc, "# EDITED personal rules") {
		t.Errorf("single diverged harness should still write back to the source:\n%s", personalSrc)
	}

	counts, err := runstate.ReadCounts(cfgDir)
	if err != nil {
		t.Fatalf("reading status counts: %v", err)
	}
	if counts.Conflicts != 0 {
		t.Errorf("one diverged harness must not be reported as a conflict, got %d", counts.Conflicts)
	}
}

func TestInstructionConflict_DifferentSections_BothWriteBackCleanly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	srcs := buildLayeredSources(t)
	p := twoTierBTargets()

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, false); err != nil {
		t.Fatalf("initial mergeAndApply: %v", err)
	}

	agentsPath := filepath.Join(home, ".codex", "AGENTS.md")
	rulesPath := filepath.Join(home, ".codeium", "windsurf", "global_rules.md")

	// codex edits "personal", windsurf edits "company" — different sections.
	writeFile(t, agentsPath, strings.Replace(readFile(t, agentsPath), "# personal rules", "# EDITED personal rules", 1))
	writeFile(t, rulesPath, strings.Replace(readFile(t, rulesPath), "# company rules", "# EDITED company rules", 1))

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, false); err != nil {
		t.Fatalf("re-apply mergeAndApply: %v", err)
	}

	personalSrc := readFile(t, filepath.Join(srcs[0].Root, "CLAUDE.md"))
	if !strings.Contains(personalSrc, "# EDITED personal rules") {
		t.Errorf("personal edit did not reach its source:\n%s", personalSrc)
	}
	companySrc := readFile(t, filepath.Join(srcs[2].Root, "CLAUDE.md"))
	if !strings.Contains(companySrc, "# EDITED company rules") {
		t.Errorf("company edit did not reach its source:\n%s", companySrc)
	}

	counts, err := runstate.ReadCounts(cfgDir)
	if err != nil {
		t.Fatalf("reading status counts: %v", err)
	}
	if counts.Conflicts != 0 {
		t.Errorf("edits to different sections must not be reported as a conflict, got %d", counts.Conflicts)
	}
}

// TestInstructionConflict_WriteBackWithoutHeldLosesAnEdit pins why the
// ordering constraint in cmd/profile.go exists: instructionWriteBack must be
// called with the held set detectInstructionConflicts computed, or the loop
// reproduces the exact last-writer-wins bug #257 fixes — the second harness
// visited silently overwrites the first harness's edit in the source, and
// nothing is reported. If cmd/profile.go is ever reordered so instrHeld is
// computed too late (or not threaded into instructionWriteBack), the full
// mergeAndApply integration test above
// (TestInstructionConflict_TwoHarnessesSameSection_HeldNotOverwritten) starts
// failing for the same reason demonstrated here in isolation.
func TestInstructionConflict_WriteBackWithoutHeldLosesAnEdit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	srcs := buildLayeredSources(t)
	p := twoTierBTargets()

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, false); err != nil {
		t.Fatalf("initial mergeAndApply: %v", err)
	}

	agentsPath := filepath.Join(home, ".codex", "AGENTS.md")
	rulesPath := filepath.Join(home, ".codeium", "windsurf", "global_rules.md")
	writeFile(t, agentsPath, strings.Replace(readFile(t, agentsPath), "# personal rules", "# personal rules\nbranch-naming rule", 1))
	writeFile(t, rulesPath, strings.Replace(readFile(t, rulesPath), "# personal rules", "# personal rules\ntest-naming rule", 1))

	instrDir := filepath.Join(cfgDir, "profiles", p.Name, "instructions")
	hReg := harness.NewRegistry(harness.Instances()...)

	// held=nil simulates write-back running without detection's result — the
	// bug this issue fixes.
	for _, target := range p.Targets {
		h, ok := hReg.Get(target)
		if !ok {
			t.Fatalf("harness %q not registered", target)
		}
		if err := instructionWriteBack(h, cfgDir, instrDir, p, srcs, nil); err != nil {
			t.Fatalf("instructionWriteBack(%s): %v", target, err)
		}
	}

	personalSrc := readFile(t, filepath.Join(srcs[0].Root, "CLAUDE.md"))
	gotBranch := strings.Contains(personalSrc, "branch-naming rule")
	gotTest := strings.Contains(personalSrc, "test-naming rule")
	if gotBranch == gotTest {
		t.Fatalf("expected exactly one edit to survive without held (last-writer-wins), got branch=%v test=%v:\n%s",
			gotBranch, gotTest, personalSrc)
	}
}

func TestMergeAndApply_preservesUserContentOutsideBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	// Pre-seed the harness file with the user's own content (no managed block).
	claudePath := filepath.Join(home, ".claude", "CLAUDE.md")
	writeFile(t, claudePath, "# MY OWN GLOBAL NOTES\nkeep this forever\n")

	srcs := buildLayeredSources(t)
	p := &profile.Profile{
		Name:    "layered",
		Sources: []string{"personal", "team", "company"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"claude-code"},
	}

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, true); err != nil {
		t.Fatalf("mergeAndApply: %v", err)
	}

	got := readFile(t, claudePath)
	if !strings.Contains(got, "# MY OWN GLOBAL NOTES") || !strings.Contains(got, "keep this forever") {
		t.Errorf("user content outside the managed block was lost:\n%s", got)
	}
	if !strings.Contains(got, "weft:begin") {
		t.Errorf("managed block not added alongside user content:\n%s", got)
	}
}
