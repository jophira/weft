package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/profile"
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
