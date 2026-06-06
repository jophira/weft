package harness

import (
	"os"
	"path/filepath"
	"testing"
)

// testCtx returns an ApplyCtx backed by a temp directory.
func testCtx(t *testing.T) ApplyCtx {
	t.Helper()
	return ApplyCtx{ProfileName: "test", CfgDir: t.TempDir()}
}

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

// ── applyWithManifest ─────────────────────────────────────────────────────────

func TestApplyWithManifest_FirstApply_NoConflict(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v1")
	write(t, filepath.Join(staged, "commands", "foo.md"), "cmd")

	target := t.TempDir()
	ctx := testCtx(t)

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(target, "CLAUDE.md")); got != "rules v1" {
		t.Errorf("CLAUDE.md = %q, want %q", got, "rules v1")
	}
	// No backup should be created on first apply.
	backupsDir := filepath.Join(ctx.CfgDir, "backups", "claude-code")
	if entries, _ := os.ReadDir(backupsDir); len(entries) > 0 {
		t.Errorf("expected no backups on first apply, got %d", len(entries))
	}
}

func TestApplyWithManifest_OwnedFile_SilentOverwrite(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v1")

	target := t.TempDir()
	ctx := testCtx(t)

	// First apply — weft takes ownership.
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil); err != nil {
		t.Fatal(err)
	}

	// Second apply with updated content — weft owns the file, no backup.
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v2")
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(target, "CLAUDE.md")); got != "rules v2" {
		t.Errorf("CLAUDE.md = %q, want %q", got, "rules v2")
	}
	backupsDir := filepath.Join(ctx.CfgDir, "backups", "claude-code")
	if entries, _ := os.ReadDir(backupsDir); len(entries) > 0 {
		t.Errorf("expected no backups when weft owns the file, got %d", len(entries))
	}
}

func TestApplyWithManifest_ExternallyModified_BackupCreated(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v1")

	target := t.TempDir()
	ctx := testCtx(t)

	// First apply — weft owns it.
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil); err != nil {
		t.Fatal(err)
	}

	// User edits the file externally.
	write(t, filepath.Join(target, "CLAUDE.md"), "my custom rules")

	// Second apply — conflict detected, backup created.
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v2")
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil); err != nil {
		t.Fatal(err)
	}

	// Target should have the new staged content.
	if got := readFile(t, filepath.Join(target, "CLAUDE.md")); got != "rules v2" {
		t.Errorf("CLAUDE.md = %q, want %q", got, "rules v2")
	}

	// Backup should contain the user's external edits.
	backupsDir := filepath.Join(ctx.CfgDir, "backups", "claude-code")
	entries, err := os.ReadDir(backupsDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 backup dir, got %d (err: %v)", len(entries), err)
	}
	backedUp := readFile(t, filepath.Join(backupsDir, entries[0].Name(), "CLAUDE.md"))
	if backedUp != "my custom rules" {
		t.Errorf("backup CLAUDE.md = %q, want %q", backedUp, "my custom rules")
	}
}

func TestApplyWithManifest_UnknownExistingFile_BackupCreated(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v1")

	target := t.TempDir()
	ctx := testCtx(t)

	// Pre-existing file in target — not in manifest (weft has never run).
	write(t, filepath.Join(target, "CLAUDE.md"), "hand-crafted rules")

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil); err != nil {
		t.Fatal(err)
	}

	// Backup should contain the pre-existing content.
	backupsDir := filepath.Join(ctx.CfgDir, "backups", "claude-code")
	entries, _ := os.ReadDir(backupsDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup dir, got %d", len(entries))
	}
	backedUp := readFile(t, filepath.Join(backupsDir, entries[0].Name(), "CLAUDE.md"))
	if backedUp != "hand-crafted rules" {
		t.Errorf("backup CLAUDE.md = %q, want %q", backedUp, "hand-crafted rules")
	}
}

func TestApplyWithManifest_SubdirBackupPreservesPath(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "commands", "backend", "java.md"), "java rules")

	target := t.TempDir()
	ctx := testCtx(t)

	// Pre-existing nested file — should be backed up with full relative path.
	write(t, filepath.Join(target, "commands", "backend", "java.md"), "old java rules")

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil); err != nil {
		t.Fatal(err)
	}

	backupsDir := filepath.Join(ctx.CfgDir, "backups", "claude-code")
	entries, _ := os.ReadDir(backupsDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup dir, got %d", len(entries))
	}
	backedUp := readFile(t, filepath.Join(backupsDir, entries[0].Name(), "commands", "backend", "java.md"))
	if backedUp != "old java rules" {
		t.Errorf("nested backup = %q, want %q", backedUp, "old java rules")
	}
}

// ── Codex ─────────────────────────────────────────────────────────────────────

func TestCodexApply_WritesAgentsMD(t *testing.T) {
	staged := seedStaged(t, "codex rules")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := (&Codex{}).Apply(staged, testCtx(t)); err != nil {
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

	if err := (&Codex{}).Apply(staged, testCtx(t)); err != nil {
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

	if err := (&Windsurf{}).Apply(staged, testCtx(t)); err != nil {
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

	if err := (&GeminiCLI{}).Apply(staged, testCtx(t)); err != nil {
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

	if err := (&Cursor{}).Apply(staged, testCtx(t)); err != nil {
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

	if err := (&Cursor{}).Apply(staged, testCtx(t)); err != nil {
		t.Fatalf("expected no error when CLAUDE.md absent, got: %v", err)
	}
}

// ── Aider ─────────────────────────────────────────────────────────────────────

func TestAiderApply_WritesConventionsMD(t *testing.T) {
	staged := seedStaged(t, "aider conventions")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := (&Aider{}).Apply(staged, testCtx(t)); err != nil {
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

	if err := (&ClaudeCode{}).Apply(staged, testCtx(t)); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(home, ".claude", "CLAUDE.md")); got != "claude rules" {
		t.Errorf("CLAUDE.md = %q, want %q", got, "claude rules")
	}
}

func TestClaudeCodeApply_UntouchedFilesNotRemoved(t *testing.T) {
	staged := seedStaged(t, "claude rules")
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Pre-existing file that weft does not stage — should survive apply.
	write(t, filepath.Join(home, ".claude", "todos.md"), "my todos")

	if err := (&ClaudeCode{}).Apply(staged, testCtx(t)); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(home, ".claude", "todos.md")); got != "my todos" {
		t.Errorf("todos.md = %q, want %q (should not be removed)", got, "my todos")
	}
}
