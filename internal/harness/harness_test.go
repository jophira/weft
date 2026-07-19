package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/testenv"
)

// testCtx returns an ApplyCtx backed by a temp directory (output discarded).
func testCtx(t *testing.T) ApplyCtx {
	t.Helper()
	return ApplyCtx{ProfileName: "test", CfgDir: t.TempDir()}
}

// testCtxWithOut returns an ApplyCtx that captures apply log output.
func testCtxWithOut(t *testing.T, buf *bytes.Buffer) ApplyCtx {
	t.Helper()
	return ApplyCtx{ProfileName: "test", CfgDir: t.TempDir(), Out: buf}
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

// ── applyWithManifest ─────────────────────────────────────────────────────────

func TestApplyWithManifest_FirstApply_NoConflict(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v1")
	write(t, filepath.Join(staged, "commands", "foo.md"), "cmd")

	target := t.TempDir()
	ctx := testCtx(t)

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
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
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Second apply with updated content — weft owns the file, no backup.
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v2")
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
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
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}

	// User edits the file externally.
	write(t, filepath.Join(target, "CLAUDE.md"), "my custom rules")

	// Second apply — conflict detected, backup created.
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v2")
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
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

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
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

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
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

func TestApplyWithManifest_UnchangedFile_Skipped(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v1")

	target := t.TempDir()
	var buf bytes.Buffer
	ctx := testCtxWithOut(t, &buf)

	// First apply — weft takes ownership.
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	buf.Reset()

	// Second apply with identical staged content — should be skipped, not rewritten.
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "· unchanged") {
		t.Errorf("expected skip log line, got: %q", out)
	}
	if strings.Contains(out, "✓ wrote") {
		t.Errorf("expected no write for unchanged file, got: %q", out)
	}
}

func TestApplyWithManifest_NewFile_LoggedAsWrote(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v1")

	target := t.TempDir()
	var buf bytes.Buffer
	ctx := testCtxWithOut(t, &buf)

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "✓ wrote") || !strings.Contains(out, "CLAUDE.md") {
		t.Errorf("expected '✓ wrote CLAUDE.md' in output, got: %q", out)
	}
}

func TestApplyWithManifest_UpdatedFile_LoggedAsWrote(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v1")

	target := t.TempDir()
	var buf bytes.Buffer
	ctx := testCtxWithOut(t, &buf)

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	buf.Reset()

	// Update staged content — weft owns the file, so no backup, just a write.
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v2")
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "✓ wrote") || !strings.Contains(out, "CLAUDE.md") {
		t.Errorf("expected '✓ wrote CLAUDE.md' for owned update, got: %q", out)
	}
	if strings.Contains(out, "· unchanged") {
		t.Errorf("unexpected skip for changed content: %q", out)
	}
}

func TestApplyWithManifest_NilOut_NoOutput(t *testing.T) {
	// ctx.Out is nil — applyOut should fall back to io.Discard, no panic.
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules")
	target := t.TempDir()
	ctx := testCtx(t) // Out is nil

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatalf("nil Out should not cause error: %v", err)
	}
}

// ── Codex ─────────────────────────────────────────────────────────────────────

func TestCodexApply_WritesAgentsMD(t *testing.T) {
	staged := seedStaged(t, "codex rules")
	home := t.TempDir()
	testenv.SetHome(t, home)

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
	testenv.SetHome(t, home)

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
	testenv.SetHome(t, home)

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
	testenv.SetHome(t, home)

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
	testenv.SetHome(t, home)

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
	testenv.SetHome(t, home)

	if err := (&Cursor{}).Apply(staged, testCtx(t)); err != nil {
		t.Fatalf("expected no error when CLAUDE.md absent, got: %v", err)
	}
}

// ── Aider ─────────────────────────────────────────────────────────────────────

func TestAiderApply_WritesConventionsMD(t *testing.T) {
	staged := seedStaged(t, "aider conventions")
	home := t.TempDir()
	testenv.SetHome(t, home)

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
	testenv.SetHome(t, home)

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
	testenv.SetHome(t, home)

	// Pre-existing file that weft does not stage — should survive apply.
	write(t, filepath.Join(home, ".claude", "todos.md"), "my todos")

	if err := (&ClaudeCode{}).Apply(staged, testCtx(t)); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(home, ".claude", "todos.md")); got != "my todos" {
		t.Errorf("todos.md = %q, want %q (should not be removed)", got, "my todos")
	}
}

// ── Source attribution in manifest ────────────────────────────────────────────

func TestApplyWithManifest_SourceAttribution_Persisted(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "merged rules")

	target := t.TempDir()
	ctx := ApplyCtx{
		ProfileName: "hybrid",
		CfgDir:      t.TempDir(),
		SourceAttribution: map[string][]string{
			"CLAUDE.md": {"work", "personal"},
		},
	}

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}

	m, err := manifest.Load(ctx.CfgDir, "claude-code")
	if err != nil {
		t.Fatalf("loading manifest: %v", err)
	}
	srcs, ok := m.SourceFiles["CLAUDE.md"]
	if !ok {
		t.Fatal("manifest.SourceFiles[CLAUDE.md] missing after apply with attribution")
	}
	if len(srcs) != 2 || srcs[0] != "work" || srcs[1] != "personal" {
		t.Errorf("SourceFiles[CLAUDE.md] = %v, want [work personal]", srcs)
	}
}

func TestApplyWithManifest_NoSourceAttribution_NoSourceFiles(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "single-source rules")

	target := t.TempDir()
	ctx := testCtx(t)

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}

	m, err := manifest.Load(ctx.CfgDir, "claude-code")
	if err != nil {
		t.Fatalf("loading manifest: %v", err)
	}
	if len(m.SourceFiles) != 0 {
		t.Errorf("expected no SourceFiles for single-source apply, got %v", m.SourceFiles)
	}
}

// ── Issue #86 — stale manifest entry pruning ──────────────────────────────────

// TestApplyWithManifest_RemovedFile_PrunedFromManifest verifies that when a file
// disappears from the staged tree, its entry is removed from the manifest so that
// a future file written at that path is not treated as a conflict.
func TestApplyWithManifest_RemovedFile_PrunedFromManifest(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "rules v1")
	write(t, filepath.Join(staged, "extra.md"), "extra content")

	target := t.TempDir()
	ctx := testCtx(t)

	// First apply — both files tracked in manifest.
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}

	m, err := manifest.Load(ctx.CfgDir, "claude-code")
	if err != nil {
		t.Fatalf("loading manifest after first apply: %v", err)
	}
	if _, ok := m.Files["extra.md"]; !ok {
		t.Fatal("expected extra.md in manifest after first apply")
	}

	// Remove extra.md from staged dir — simulates a source file being deleted.
	if err := os.Remove(filepath.Join(staged, "extra.md")); err != nil {
		t.Fatal(err)
	}

	// Second apply — extra.md is not staged; it must be pruned from manifest.
	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}

	m2, err := manifest.Load(ctx.CfgDir, "claude-code")
	if err != nil {
		t.Fatalf("loading manifest after second apply: %v", err)
	}
	if _, ok := m2.Files["extra.md"]; ok {
		t.Error("extra.md must be pruned from manifest after it is removed from staged tree")
	}
}

// TestApplyWithManifest_SourceFiles_PrunedWhenFileRemoved verifies that SourceFiles
// entries for removed staged files are also pruned on the next apply.
func TestApplyWithManifest_SourceFiles_PrunedWhenFileRemoved(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "merged rules")
	write(t, filepath.Join(staged, "extra.md"), "extra")

	target := t.TempDir()
	ctx := ApplyCtx{
		ProfileName: "hybrid",
		CfgDir:      t.TempDir(),
		SourceAttribution: map[string][]string{
			"CLAUDE.md": {"work", "personal"},
			"extra.md":  {"work"},
		},
	}

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Remove extra.md from staged; also drop it from SourceAttribution.
	if err := os.Remove(filepath.Join(staged, "extra.md")); err != nil {
		t.Fatal(err)
	}
	ctx.SourceAttribution = map[string][]string{
		"CLAUDE.md": {"work", "personal"},
	}

	if err := applyWithManifest(staged, target, "claude-code", ctx, nil, nil); err != nil {
		t.Fatal(err)
	}

	m, err := manifest.Load(ctx.CfgDir, "claude-code")
	if err != nil {
		t.Fatalf("loading manifest: %v", err)
	}
	if _, ok := m.SourceFiles["extra.md"]; ok {
		t.Error("extra.md must be pruned from SourceFiles after it is removed from staged tree")
	}
	if srcs, ok := m.SourceFiles["CLAUDE.md"]; !ok || len(srcs) != 2 {
		t.Errorf("CLAUDE.md SourceFiles = %v, want [work personal]", srcs)
	}
}

// ── Warp ──────────────────────────────────────────────────────────────────────

// TestWarpApply_CopiesYAMLToWorkflows verifies that Apply writes .yaml files from
// commands/ into <configRoot>/workflows/ and ignores non-YAML files.
func TestWarpApply_CopiesYAMLToWorkflows(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "commands", "build.yaml"), "name: build")
	write(t, filepath.Join(staged, "commands", "test.yml"), "name: test")
	write(t, filepath.Join(staged, "commands", "notes.txt"), "should be ignored")
	write(t, filepath.Join(staged, "CLAUDE.md"), "should be ignored too")

	configRoot := t.TempDir()
	w := &Warp{configRoot: configRoot}

	if err := w.Apply(staged, testCtx(t)); err != nil {
		t.Fatal(err)
	}

	workflows := filepath.Join(configRoot, "workflows")
	if got := readFile(t, filepath.Join(workflows, "build.yaml")); got != "name: build" {
		t.Errorf("build.yaml = %q, want %q", got, "name: build")
	}
	if got := readFile(t, filepath.Join(workflows, "test.yml")); got != "name: test" {
		t.Errorf("test.yml = %q, want %q", got, "name: test")
	}
	if _, err := os.Stat(filepath.Join(workflows, "notes.txt")); err == nil {
		t.Error("notes.txt should not be copied to workflows/")
	}
	if _, err := os.Stat(filepath.Join(workflows, "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md should not be copied to workflows/")
	}
}

// TestWarpApply_SkipUnchanged verifies the fe.skip optimisation — writing identical
// content twice should log "· unchanged" on the second apply, not "✓ wrote".
func TestWarpApply_SkipUnchanged(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "commands", "build.yaml"), "name: build")

	configRoot := t.TempDir()
	w := &Warp{configRoot: configRoot}

	var buf bytes.Buffer
	ctx := testCtxWithOut(t, &buf)

	// First apply — writes the file.
	if err := w.Apply(staged, ctx); err != nil {
		t.Fatal(err)
	}
	buf.Reset()

	// Second apply — identical staged content; should be skipped.
	if err := w.Apply(staged, ctx); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "· unchanged") {
		t.Errorf("expected '· unchanged' on second apply of unchanged file, got: %q", out)
	}
	if strings.Contains(out, "✓ wrote") {
		t.Errorf("unexpected '✓ wrote' for unchanged file: %q", out)
	}
}

// TestWarpApply_ConflictWrittenToCtxOut verifies that conflict messages are sent to
// ctx.Out (not fmt.Printf/stdout), which prevents corruption of the MCP JSON-RPC wire.
func TestWarpApply_ConflictWrittenToCtxOut(t *testing.T) {
	staged := t.TempDir()
	write(t, filepath.Join(staged, "commands", "build.yaml"), "name: build v1")

	configRoot := t.TempDir()
	w := &Warp{configRoot: configRoot}

	ctx := testCtx(t)

	// First apply — weft takes ownership.
	if err := w.Apply(staged, ctx); err != nil {
		t.Fatal(err)
	}

	// User modifies the file externally.
	write(t, filepath.Join(configRoot, "workflows", "build.yaml"), "name: hand-edited")

	// Second apply with new staged content — conflict should be reported to ctx.Out.
	write(t, filepath.Join(staged, "commands", "build.yaml"), "name: build v2")

	var buf bytes.Buffer
	ctx.Out = &buf

	if err := w.Apply(staged, ctx); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "externally modified") {
		t.Errorf("expected conflict message in ctx.Out, got: %q", out)
	}
}

// TestWarpYAMLFilter verifies the filter rejects subdirectory entries and non-YAML files.
func TestWarpYAMLFilter(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"build.yaml", true},
		{"test.yml", true},
		{"notes.txt", false},
		{"subdir/build.yaml", false}, // flat copy — subdirs excluded
		{"README.md", false},
	}
	for _, tc := range cases {
		if got := warpYAMLFilter(tc.rel); got != tc.want {
			t.Errorf("warpYAMLFilter(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

// ── Registry ──────────────────────────────────────────────────────────────────

type stubHarness struct{ name string }

func (s *stubHarness) Name() string                     { return s.name }
func (s *stubHarness) Detect() bool                     { return false }
func (s *stubHarness) Apply(_ string, _ ApplyCtx) error { return nil }

func TestNewRegistry_notNil(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry() returned nil")
	}
}

func TestRegistry_Get_found(t *testing.T) {
	r := NewRegistry(&stubHarness{"claude-code"}, &stubHarness{"codex"})
	h, ok := r.Get("codex")
	if !ok {
		t.Fatal("Get(codex): expected ok=true")
	}
	if h.Name() != "codex" {
		t.Errorf("Get(codex).Name() = %q, want codex", h.Name())
	}
}

func TestRegistry_Get_notFound(t *testing.T) {
	r := NewRegistry(&stubHarness{"claude-code"})
	if _, ok := r.Get("nonexistent"); ok {
		t.Error("Get(nonexistent): expected ok=false")
	}
}

func TestRegistry_Detect_empty(t *testing.T) {
	// All stubs return Detect()=false, so Detect() returns empty slice.
	r := NewRegistry(&stubHarness{"a"}, &stubHarness{"b"})
	detected := r.Detect()
	if len(detected) != 0 {
		t.Errorf("Detect() = %v, want empty (all stubs return false)", detected)
	}
}

// ── All / Instances ───────────────────────────────────────────────────────────

func TestAll_returnsBuiltins(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("All() returned empty list; expected built-in harnesses")
	}
}

func TestInstances_lengthMatchesAll(t *testing.T) {
	if len(Instances()) != len(All()) {
		t.Errorf("Instances() len=%d != All() len=%d", len(Instances()), len(All()))
	}
}

func TestInstances_allHaveNames(t *testing.T) {
	for _, h := range Instances() {
		if h.Name() == "" {
			t.Error("Instances() returned harness with empty name")
		}
	}
}
