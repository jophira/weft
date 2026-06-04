package harness

import (
	"os"
	"path/filepath"
	"testing"
)

// seedStaged creates a minimal staged directory with CLAUDE.md and one extra file.
func seedStaged(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "CLAUDE.md"), content)
	write(t, filepath.Join(dir, "commands", "foo.yaml"), "name: foo")
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// ── copyWithRename ────────────────────────────────────────────────────────────

func TestCopyWithRename_NoRename(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "CLAUDE.md"), "hello")
	write(t, filepath.Join(src, "commands", "foo.yaml"), "x: 1")

	dst := t.TempDir()
	if err := copyWithRename(src, dst, nil); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(dst, "CLAUDE.md")); got != "hello" {
		t.Errorf("CLAUDE.md content = %q, want %q", got, "hello")
	}
	if got := readFile(t, filepath.Join(dst, "commands", "foo.yaml")); got != "x: 1" {
		t.Errorf("commands/foo.yaml content = %q, want %q", got, "x: 1")
	}
}

func TestCopyWithRename_Renamed(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "CLAUDE.md"), "instructions")
	write(t, filepath.Join(src, "commands", "foo.yaml"), "x: 1")

	dst := t.TempDir()
	renames := map[string]string{"CLAUDE.md": "AGENTS.md"}
	if err := copyWithRename(src, dst, renames); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dst, "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md should not exist in dst after rename")
	}
	if got := readFile(t, filepath.Join(dst, "AGENTS.md")); got != "instructions" {
		t.Errorf("AGENTS.md content = %q, want %q", got, "instructions")
	}
	// non-renamed files should still be present
	if got := readFile(t, filepath.Join(dst, "commands", "foo.yaml")); got != "x: 1" {
		t.Errorf("commands/foo.yaml content = %q, want %q", got, "x: 1")
	}
}

func TestCopyWithRename_EmptySourceDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := copyWithRename(src, dst, nil); err != nil {
		t.Fatal(err)
	}
}

// ── Codex ─────────────────────────────────────────────────────────────────────

func TestCodexApply_WritesAgentsMD(t *testing.T) {
	staged := seedStaged(t, "codex rules")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := (&Codex{}).Apply(staged); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(home, ".codex", "AGENTS.md")); got != "codex rules" {
		t.Errorf("AGENTS.md = %q, want %q", got, "codex rules")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md should not be written to ~/.codex")
	}
}

func TestCodexApply_PreservesOtherFiles(t *testing.T) {
	staged := seedStaged(t, "rules")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := (&Codex{}).Apply(staged); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(home, ".codex", "commands", "foo.yaml")); err != nil {
		t.Errorf("commands/foo.yaml should be present: %v", err)
	}
}

// ── Windsurf ──────────────────────────────────────────────────────────────────

func TestWindsurfApply_WritesGlobalRules(t *testing.T) {
	staged := seedStaged(t, "windsurf rules")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := (&Windsurf{}).Apply(staged); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, filepath.Join(home, ".codeium", "windsurf", "global_rules.md"))
	if got != "windsurf rules" {
		t.Errorf("global_rules.md = %q, want %q", got, "windsurf rules")
	}
	if _, err := os.Stat(filepath.Join(home, ".codeium", "windsurf", "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md should not be written to ~/.codeium/windsurf")
	}
}

// ── GeminiCLI ─────────────────────────────────────────────────────────────────

func TestGeminiCLIApply_WritesGeminiMD(t *testing.T) {
	staged := seedStaged(t, "gemini rules")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := (&GeminiCLI{}).Apply(staged); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, filepath.Join(home, ".gemini", "GEMINI.md"))
	if got != "gemini rules" {
		t.Errorf("GEMINI.md = %q, want %q", got, "gemini rules")
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md should not be written to ~/.gemini")
	}
}

// ── Cursor ────────────────────────────────────────────────────────────────────

func TestCursorApply_WritesMDCWithFrontmatter(t *testing.T) {
	staged := seedStaged(t, "cursor rules")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := (&Cursor{}).Apply(staged); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, filepath.Join(home, ".cursor", "rules", "weft.mdc"))
	want := cursorMDCHeader + "cursor rules"
	if got != want {
		t.Errorf("weft.mdc =\n%q\nwant\n%q", got, want)
	}
}

func TestCursorApply_NoCLAUDEMD_NoError(t *testing.T) {
	staged := t.TempDir() // no CLAUDE.md
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := (&Cursor{}).Apply(staged); err != nil {
		t.Fatalf("expected no error when CLAUDE.md absent, got: %v", err)
	}
}

// ── Aider ─────────────────────────────────────────────────────────────────────

func TestAiderApply_WritesConventionsMD(t *testing.T) {
	staged := seedStaged(t, "aider conventions")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := (&Aider{}).Apply(staged); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, filepath.Join(home, ".aider", "CONVENTIONS.md"))
	if got != "aider conventions" {
		t.Errorf("CONVENTIONS.md = %q, want %q", got, "aider conventions")
	}
	if _, err := os.Stat(filepath.Join(home, ".aider", "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md should not be written to ~/.aider")
	}
}

// ── ClaudeCode (regression) ───────────────────────────────────────────────────

func TestClaudeCodeApply_PreservesFilename(t *testing.T) {
	staged := seedStaged(t, "claude rules")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := (&ClaudeCode{}).Apply(staged); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(home, ".claude", "CLAUDE.md")); got != "claude rules" {
		t.Errorf("CLAUDE.md = %q, want %q", got, "claude rules")
	}
}
