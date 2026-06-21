package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/instruction"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/testenv"
)

// stubInstructed is a Harness that consumes a root instruction file at a
// caller-controlled path — lets us exercise ProjectInstruction without touching
// a real harness config dir.
type stubInstructed struct {
	name string
	spec InstructionSpec
}

func (s *stubInstructed) Name() string                 { return s.name }
func (s *stubInstructed) Detect() bool                 { return true }
func (s *stubInstructed) Apply(string, ApplyCtx) error { return nil }
func (s *stubInstructed) InstructionSpec() (InstructionSpec, error) {
	return s.spec, nil
}

// plainHarness implements Harness but NOT InstructionConsumer.
type plainHarness struct{}

func (plainHarness) Name() string                 { return "plain" }
func (plainHarness) Detect() bool                 { return true }
func (plainHarness) Apply(string, ApplyCtx) error { return nil }

func projectCtx(t *testing.T) ApplyCtx {
	t.Helper()
	return ApplyCtx{ProfileName: "test", CfgDir: t.TempDir()}
}

func TestProjectInstruction_importWritesForwardSlashImports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	h := &stubInstructed{name: "claudish", spec: InstructionSpec{
		Path: path, Strategy: StrategyImport, ImportTemplate: "@{path}",
	}}
	ctx := projectCtx(t)
	// CopyPaths use native separators; the emitted import lines must be /-slashed.
	sources := []SourceInstruction{
		{Name: "personal", CopyPath: filepath.Join("w", "10-personal.md")},
		{Name: "company", CopyPath: filepath.Join("w", "30-company.md")},
	}

	if err := ProjectInstruction(h, sources, ctx); err != nil {
		t.Fatalf("ProjectInstruction: %v", err)
	}

	got := readGolden(t, path)
	if !strings.Contains(got, instruction.BlockBegin) || !strings.Contains(got, instruction.BlockEnd) {
		t.Errorf("managed block markers missing:\n%s", got)
	}
	for _, frag := range []string{"@w/10-personal.md", "@w/30-company.md"} {
		if !strings.Contains(got, frag) {
			t.Errorf("missing import %q in:\n%s", frag, got)
		}
	}
	if strings.Contains(got, "\\") {
		t.Errorf("import lines must use forward slashes, got backslash:\n%s", got)
	}
	if i, j := strings.Index(got, "10-personal"), strings.Index(got, "30-company"); i > j {
		t.Error("imports out of priority order (personal should precede company)")
	}

	m, _ := manifest.Load(ctx.CfgDir, "claudish")
	if m.InstructionPath != path {
		t.Errorf("manifest InstructionPath = %q, want %q", m.InstructionPath, path)
	}
	if m.InstructionBlock == "" {
		t.Error("manifest InstructionBlock not recorded")
	}
}

func TestProjectInstruction_inlineEmbedsAttributedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	h := &stubInstructed{name: "codish", spec: InstructionSpec{Path: path, Strategy: StrategyInline}}
	sources := []SourceInstruction{
		{Name: "personal", Content: "personal rules"},
		{Name: "company", Content: "company rules"},
	}

	if err := ProjectInstruction(h, sources, projectCtx(t)); err != nil {
		t.Fatalf("ProjectInstruction: %v", err)
	}

	got := readGolden(t, path)
	for _, frag := range []string{
		`<!-- weft:source:begin name="personal" -->`, "personal rules",
		`<!-- weft:source:begin name="company" -->`, "company rules",
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("missing %q in:\n%s", frag, got)
		}
	}
}

func TestProjectInstruction_preservesOutsideAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# MY OWN NOTES\nkeep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &stubInstructed{name: "h", spec: InstructionSpec{Path: path, Strategy: StrategyInline}}
	sources := []SourceInstruction{{Name: "s", Content: "rule"}}
	ctx := projectCtx(t)

	if err := ProjectInstruction(h, sources, ctx); err != nil {
		t.Fatal(err)
	}
	first := readGolden(t, path)
	if !strings.Contains(first, "# MY OWN NOTES") || !strings.Contains(first, "keep me") {
		t.Errorf("user content outside block was lost:\n%s", first)
	}

	if err := ProjectInstruction(h, sources, ctx); err != nil {
		t.Fatal(err)
	}
	if second := readGolden(t, path); second != first {
		t.Errorf("not idempotent:\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestProjectInstruction_seedsPreambleForNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.mdc")
	h := &stubInstructed{name: "cursorish", spec: InstructionSpec{
		Path: path, Strategy: StrategyInline, Preamble: "---\nalwaysApply: true\n---\n",
	}}
	if err := ProjectInstruction(h, []SourceInstruction{{Name: "s", Content: "rule"}}, projectCtx(t)); err != nil {
		t.Fatal(err)
	}
	if got := readGolden(t, path); !strings.HasPrefix(got, "---\nalwaysApply: true\n---\n") {
		t.Errorf("preamble not seeded:\n%s", got)
	}
}

func TestProjectInstruction_noInstructionFileIsNoOp(t *testing.T) {
	if err := ProjectInstruction(plainHarness{}, []SourceInstruction{{Name: "s", Content: "x"}}, projectCtx(t)); err != nil {
		t.Errorf("expected no-op nil for harness without an instruction file, got %v", err)
	}
}

func TestAdapterInstructionSpecs(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	cases := []struct {
		h          InstructionConsumer
		wantSuffix string
		strategy   Strategy
	}{
		{&ClaudeCode{}, filepath.Join(".claude", "CLAUDE.md"), StrategyImport},
		{&GeminiCLI{}, filepath.Join(".gemini", "GEMINI.md"), StrategyImport},
		{&Codex{}, filepath.Join(".codex", "AGENTS.md"), StrategyInline},
		{&Windsurf{}, filepath.Join(".codeium", "windsurf", "global_rules.md"), StrategyInline},
		{&Aider{}, filepath.Join(".aider", "CONVENTIONS.md"), StrategyInline},
		{&Cursor{}, filepath.Join(".cursor", "rules", "weft.mdc"), StrategyInline},
	}
	for _, c := range cases {
		spec, err := c.h.InstructionSpec()
		if err != nil {
			t.Errorf("%T: %v", c.h, err)
			continue
		}
		if !strings.HasSuffix(spec.Path, c.wantSuffix) {
			t.Errorf("%T path = %q, want suffix %q", c.h, spec.Path, c.wantSuffix)
		}
		if spec.Strategy != c.strategy {
			t.Errorf("%T strategy = %q, want %q", c.h, spec.Strategy, c.strategy)
		}
	}
}

func TestWarpHasNoInstructionFile(t *testing.T) {
	if _, ok := Harness(&Warp{}).(InstructionConsumer); ok {
		t.Error("Warp should not implement InstructionConsumer (no root instruction file)")
	}
}

// readGolden reads a file for assertion, failing the test on error.
func readGolden(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}
