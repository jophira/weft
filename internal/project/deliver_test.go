package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/instruction"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// ── ReadInputs ────────────────────────────────────────────────────────────────

func TestReadInputs_readsAuthoredContent(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "CLAUDE.md"), "# Rules\n\nBe brief.\n")

	got, err := ReadInputs(root, []string{"CLAUDE.md"}, nil)
	if err != nil {
		t.Fatalf("ReadInputs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d inputs, want 1", len(got))
	}
	if got[0].Rel != "CLAUDE.md" || !strings.Contains(got[0].Content, "Be brief.") {
		t.Errorf("input = %+v, want CLAUDE.md with its content", got[0])
	}
}

// The loop this guards against: weft writes a block into a file, then reads it
// back as if the user wrote it, and every rule duplicates on each pass.
func TestReadInputs_stripsWeftsOwnBlock(t *testing.T) {
	root := t.TempDir()
	authored := "# My rules\n\nAlways use tabs.\n"
	withBlock := string(instruction.Upsert([]byte(authored), "IMPORTED CONTENT FROM WEFT"))
	write(t, filepath.Join(root, "AGENTS.md"), withBlock)

	got, err := ReadInputs(root, []string{"AGENTS.md"}, nil)
	if err != nil {
		t.Fatalf("ReadInputs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d inputs, want 1", len(got))
	}
	if strings.Contains(got[0].Content, "IMPORTED CONTENT FROM WEFT") {
		t.Errorf("content still holds weft's own block:\n%s", got[0].Content)
	}
	if !strings.Contains(got[0].Content, "Always use tabs.") {
		t.Errorf("content lost the user's own text:\n%s", got[0].Content)
	}
}

func TestReadInputs_skipsFilesWeftDeliversTo(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "CLAUDE.md"), "# Keep\n\nkeep me\n")
	write(t, filepath.Join(root, "AGENTS.md"), "# Written by weft\n\nweft output\n")

	got, err := ReadInputs(root, []string{"CLAUDE.md", "AGENTS.md"}, []string{"AGENTS.md"})
	if err != nil {
		t.Fatalf("ReadInputs: %v", err)
	}
	if len(got) != 1 || got[0].Rel != "CLAUDE.md" {
		t.Errorf("inputs = %+v, want only CLAUDE.md", got)
	}
}

func TestReadInputs_emptyAndMissingContributeNothing(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "CLAUDE.md"), "   \n\n")

	got, err := ReadInputs(root, []string{"CLAUDE.md", "GEMINI.md"}, nil)
	if err != nil {
		t.Fatalf("ReadInputs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("inputs = %+v, want none", got)
	}
}

func TestReadInputs_expandsGlobs(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".cursor", "rules", "a.mdc"), "rule a\n")
	write(t, filepath.Join(root, ".cursor", "rules", "b.mdc"), "rule b\n")

	got, err := ReadInputs(root, []string{".cursor/rules/*.mdc"}, nil)
	if err != nil {
		t.Fatalf("ReadInputs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d inputs, want 2", len(got))
	}
	if got[0].Rel != ".cursor/rules/a.mdc" {
		t.Errorf("first input = %q, want sorted order", got[0].Rel)
	}
}

func TestReadInputs_deduplicatesAcrossPatterns(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "CLAUDE.md"), "once\n")
	got, err := ReadInputs(root, []string{"CLAUDE.md", "./CLAUDE.md"}, nil)
	if err != nil {
		t.Fatalf("ReadInputs: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d inputs, want 1 — the same file matched twice", len(got))
	}
}

// ── WriteParts ────────────────────────────────────────────────────────────────

func TestWriteParts_writesNumberedFilesInOrder(t *testing.T) {
	root := t.TempDir()
	parts := []Part{
		{Name: "weft-rules", Origin: OriginRules, Content: "global rules"},
		{Name: "CLAUDE.md", Origin: OriginInput, Content: "repo rules"},
	}
	written, err := WriteParts(root, parts)
	if err != nil {
		t.Fatalf("WriteParts: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("wrote %d parts, want 2", len(written))
	}
	if base := filepath.Base(written[0].Path); base != "00-weft-rules.md" {
		t.Errorf("first file = %q, want 00-weft-rules.md", base)
	}
	// A repo-relative name carries a dot and an extension. It must not become a
	// directory, and must not end up doubly suffixed.
	if base := filepath.Base(written[1].Path); base != "10-claude.md" {
		t.Errorf("second file = %q, want the flattened name without a doubled extension", base)
	}
	if body := read(t, written[0].Path); !strings.Contains(body, generatedHeader) {
		t.Errorf("body %q missing the generated header", body)
	}
}

// Stale parts left behind would be imported forever by a harness that never
// asked for them, so the directory is rebuilt rather than merged.
func TestWriteParts_rebuildsRatherThanMerging(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteParts(root, []Part{{Name: "old", Content: "gone"}, {Name: "other", Content: "also gone"}}); err != nil {
		t.Fatalf("first WriteParts: %v", err)
	}
	if _, err := WriteParts(root, []Part{{Name: "new", Content: "kept"}}); err != nil {
		t.Fatalf("second WriteParts: %v", err)
	}
	entries, err := os.ReadDir(InstructionsDir(root))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "00-new.md" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("dir holds %v, want only 00-new.md", names)
	}
}

func TestWriteParts_noPartsLeavesNoDirectory(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteParts(root, nil); err != nil {
		t.Fatalf("WriteParts: %v", err)
	}
	if _, err := os.Stat(InstructionsDir(root)); !os.IsNotExist(err) {
		t.Error("instructions dir exists with no parts")
	}
}

// ── exclusion of a harness's own input ────────────────────────────────────────

func TestImportPathsFor_skipsTheHarnessOwnInput(t *testing.T) {
	written := []WrittenPart{
		{Part: Part{Name: "weft-rules", Origin: OriginRules}, Path: "/r/.weft/instructions/00-weft-rules.md"},
		{Part: Part{Name: "CLAUDE.md", Origin: OriginInput}, Path: "/r/.weft/instructions/10-claude.md.md"},
		{Part: Part{Name: "AGENTS.md", Origin: OriginInput}, Path: "/r/.weft/instructions/20-agents.md.md"},
	}
	// Claude Code already reads the repository's CLAUDE.md, so importing a copy
	// would show the model every rule in it twice.
	got := ImportPathsFor(written, []string{"CLAUDE.md"})
	if len(got) != 2 {
		t.Fatalf("got %d paths, want 2", len(got))
	}
	for _, p := range got {
		if strings.Contains(p, "10-claude") {
			t.Errorf("paths %v still include the harness's own input", got)
		}
	}
}

func TestImportPathsFor_alwaysKeepsResolverRules(t *testing.T) {
	written := []WrittenPart{
		{Part: Part{Name: "weft-rules", Origin: OriginRules}, Path: "/r/00-weft-rules.md"},
	}
	// Rules come from weft's sources, so no harness has them already.
	if got := ImportPathsFor(written, []string{"weft-rules"}); len(got) != 1 {
		t.Errorf("got %d paths, want the resolver rules kept regardless", len(got))
	}
}

// ── DeliverHook ───────────────────────────────────────────────────────────────

func TestDeliverHook_createsSettingsWithHook(t *testing.T) {
	root := t.TempDir()
	wrote, err := DeliverHook(root, ".claude/settings.local.json")
	if err != nil {
		t.Fatalf("DeliverHook: %v", err)
	}
	if !wrote {
		t.Error("reported no write on a fresh settings file")
	}
	body := read(t, filepath.Join(root, ".claude", "settings.local.json"))
	if !strings.Contains(body, HookCommand) {
		t.Errorf("settings = %s, want the resolver command", body)
	}
}

func TestDeliverHook_isIdempotent(t *testing.T) {
	root := t.TempDir()
	if _, err := DeliverHook(root, ".claude/settings.local.json"); err != nil {
		t.Fatalf("first: %v", err)
	}
	wrote, err := DeliverHook(root, ".claude/settings.local.json")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if wrote {
		t.Error("second call wrote again, want idempotent")
	}
}

// The settings file belongs to the user and may hold permissions, environment
// and other hooks. Replacing it would be a data loss.
func TestDeliverHook_preservesTheUsersOwnSettings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "settings.local.json")
	write(t, path, `{"permissions":{"allow":["Bash(ls:*)"]},"env":{"FOO":"bar"}}`)

	if _, err := DeliverHook(root, ".claude/settings.local.json"); err != nil {
		t.Fatalf("DeliverHook: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(read(t, path)), &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if doc["permissions"] == nil || doc["env"] == nil {
		t.Errorf("settings lost the user's own keys: %v", doc)
	}
	if doc["hooks"] == nil {
		t.Error("settings gained no hook")
	}
}

func TestDeliverHook_keepsExistingSessionStartHooks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "settings.local.json")
	write(t, path, `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"echo hi"}]}]}}`)

	if _, err := DeliverHook(root, ".claude/settings.local.json"); err != nil {
		t.Fatalf("DeliverHook: %v", err)
	}
	body := read(t, path)
	if !strings.Contains(body, "echo hi") {
		t.Errorf("settings = %s, lost the pre-existing hook", body)
	}
	if !strings.Contains(body, HookCommand) {
		t.Errorf("settings = %s, missing weft's hook", body)
	}
}

func TestDeliverHook_refusesInvalidJSONRatherThanOverwriting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "settings.local.json")
	original := `{"permissions": oops`
	write(t, path, original)

	if _, err := DeliverHook(root, ".claude/settings.local.json"); err == nil {
		t.Error("DeliverHook accepted invalid JSON, want a refusal")
	}
	if got := read(t, path); got != original {
		t.Errorf("file was modified despite the refusal:\n%s", got)
	}
}

// ── DeliverImport ─────────────────────────────────────────────────────────────

func TestDeliverImport_writesRepoRelativeLines(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, ".weft", "instructions", "00-weft-rules.md")}

	wrote, err := DeliverImport(root, "GEMINI.md", "@{path}", paths)
	if err != nil {
		t.Fatalf("DeliverImport: %v", err)
	}
	if !wrote {
		t.Error("reported no write")
	}
	body := read(t, filepath.Join(root, "GEMINI.md"))
	// Repo-relative so the line survives the repository moving, and stays inside
	// the directory an import-path validator will allow.
	if !strings.Contains(body, "@./.weft/instructions/00-weft-rules.md") {
		t.Errorf("GEMINI.md = %s, want a repo-relative import line", body)
	}
	if strings.Contains(body, root) {
		t.Errorf("GEMINI.md = %s, want no absolute path", body)
	}
}

func TestDeliverImport_preservesAuthoredContent(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "GEMINI.md"), "# Mine\n\nDo not touch this.\n")

	if _, err := DeliverImport(root, "GEMINI.md", "@{path}", []string{filepath.Join(root, "x.md")}); err != nil {
		t.Fatalf("DeliverImport: %v", err)
	}
	body := read(t, filepath.Join(root, "GEMINI.md"))
	if !strings.Contains(body, "Do not touch this.") {
		t.Errorf("GEMINI.md = %s, lost the user's content", body)
	}
}

// An unchanged rewrite still shows as modified in git status, which would train
// the user to ignore weft's diffs.
func TestDeliverImport_secondCallWritesNothing(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, ".weft", "instructions", "00-weft-rules.md")}
	if _, err := DeliverImport(root, "GEMINI.md", "@{path}", paths); err != nil {
		t.Fatalf("first: %v", err)
	}
	wrote, err := DeliverImport(root, "GEMINI.md", "@{path}", paths)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if wrote {
		t.Error("second identical delivery rewrote the file")
	}
}

// ── DeliverInline ─────────────────────────────────────────────────────────────

func TestDeliverInline_writesContentWithAttribution(t *testing.T) {
	root := t.TempDir()
	parts := []Part{{Name: "CLAUDE.md", Origin: OriginInput, Content: "Be brief."}}

	if _, err := DeliverInline(root, "AGENTS.md", "", parts); err != nil {
		t.Fatalf("DeliverInline: %v", err)
	}
	body := read(t, filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(body, "Be brief.") {
		t.Errorf("AGENTS.md = %s, missing the content", body)
	}
	if !strings.Contains(body, `name="CLAUDE.md"`) {
		t.Errorf("AGENTS.md = %s, missing attribution", body)
	}
}

func TestDeliverInline_seedsPreambleOnFirstCreate(t *testing.T) {
	root := t.TempDir()
	preamble := "---\nalwaysApply: true\n---\n"
	parts := []Part{{Name: "x", Content: "content"}}

	if _, err := DeliverInline(root, ".cursor/rules/weft.mdc", preamble, parts); err != nil {
		t.Fatalf("DeliverInline: %v", err)
	}
	body := read(t, filepath.Join(root, ".cursor", "rules", "weft.mdc"))
	if !strings.HasPrefix(body, "---\nalwaysApply: true\n---") {
		t.Errorf("weft.mdc = %s, want the frontmatter preamble first", body)
	}
}
