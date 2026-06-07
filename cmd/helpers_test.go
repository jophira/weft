package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/source"
	"github.com/spf13/cobra"
)

// ── fmtBytes ──────────────────────────────────────────────────────────────────

func TestFmtBytes_lessThan1KB(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{512, "512 B"},
		{1023, "1023 B"},
	}
	for _, tt := range tests {
		if got := fmtBytes(tt.n); got != tt.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFmtBytes_1KBAndAbove(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{1024, "1.0 KB"},
		{2048, "2.0 KB"},
		{1536, "1.5 KB"},
	}
	for _, tt := range tests {
		if got := fmtBytes(tt.n); got != tt.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// ── mermaidNodeID ─────────────────────────────────────────────────────────────

func TestMermaidNodeID_alnum(t *testing.T) {
	if got := mermaidNodeID("hello123"); got != "hello123" {
		t.Errorf("mermaidNodeID(%q) = %q, want %q", "hello123", got, "hello123")
	}
}

func TestMermaidNodeID_specialCharsReplaced(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"hello world", "hello_world"},
		{"my-source", "my_source"},
		{"a/b/c", "a_b_c"},
		{"", ""},
		{"kebab-case_2", "kebab_case_2"},
	}
	for _, tt := range tests {
		if got := mermaidNodeID(tt.in); got != tt.want {
			t.Errorf("mermaidNodeID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ── parseSources ──────────────────────────────────────────────────────────────

func TestParseSources_commaSeparated(t *testing.T) {
	got := parseSources("work,personal,community")
	if len(got) != 3 {
		t.Fatalf("parseSources len = %d, want 3; got %v", len(got), got)
	}
	if got[0] != "work" || got[1] != "personal" || got[2] != "community" {
		t.Errorf("parseSources = %v, unexpected", got)
	}
}

func TestParseSources_tripsWhitespace(t *testing.T) {
	got := parseSources("  work , personal  ")
	if len(got) != 2 || got[0] != "work" || got[1] != "personal" {
		t.Errorf("parseSources = %v, want [work personal]", got)
	}
}

func TestParseSources_empty(t *testing.T) {
	got := parseSources("")
	if len(got) != 0 {
		t.Errorf("parseSources(%q) = %v, want empty", "", got)
	}
}

func TestParseSources_single(t *testing.T) {
	got := parseSources("work")
	if len(got) != 1 || got[0] != "work" {
		t.Errorf("parseSources(%q) = %v, want [work]", "work", got)
	}
}

func TestParseSources_skipsEmptySegments(t *testing.T) {
	got := parseSources("work,,personal")
	if len(got) != 2 {
		t.Errorf("parseSources with empty segment: len=%d, want 2; got %v", len(got), got)
	}
}

// ── boolWord ──────────────────────────────────────────────────────────────────

func TestBoolWord(t *testing.T) {
	if got := boolWord(true); got != "yes" {
		t.Errorf("boolWord(true) = %q, want yes", got)
	}
	if got := boolWord(false); got != "no" {
		t.Errorf("boolWord(false) = %q, want no", got)
	}
}

// ── isReadOnlyCmd ─────────────────────────────────────────────────────────────

func TestIsReadOnlyCmd_readOnlyNames(t *testing.T) {
	readOnly := []string{"list", "status", "backups", "version", "help", "diff", "sync", "push"}
	for _, name := range readOnly {
		cmd := &cobra.Command{Use: name}
		if !isReadOnlyCmd(cmd) {
			t.Errorf("isReadOnlyCmd(%q) = false, want true", name)
		}
	}
}

func TestIsReadOnlyCmd_nonReadOnly(t *testing.T) {
	names := []string{"apply", "add", "create", "delete", "use"}
	for _, name := range names {
		cmd := &cobra.Command{Use: name}
		if isReadOnlyCmd(cmd) {
			t.Errorf("isReadOnlyCmd(%q) = true, want false", name)
		}
	}
}

// ── validateOverlay ───────────────────────────────────────────────────────────

func TestValidateOverlay_valid(t *testing.T) {
	for _, v := range []string{"cascade", "merge", "last-wins"} {
		if err := validateOverlay(v); err != nil {
			t.Errorf("validateOverlay(%q) = %v, want nil", v, err)
		}
	}
}

func TestValidateOverlay_invalid(t *testing.T) {
	if err := validateOverlay("unknown"); err == nil {
		t.Error("validateOverlay(unknown): expected error, got nil")
	}
}

// ── validateTarget ────────────────────────────────────────────────────────────

func TestValidateTarget_knownHarness(t *testing.T) {
	// "claude-code" is always a built-in harness.
	if err := validateTarget("claude-code"); err != nil {
		t.Errorf("validateTarget(claude-code) = %v, want nil", err)
	}
}

func TestValidateTarget_unknownHarness(t *testing.T) {
	if err := validateTarget("definitely-not-a-real-harness-xyz"); err == nil {
		t.Error("validateTarget(unknown): expected error, got nil")
	}
}

// ── countFiles ────────────────────────────────────────────────────────────────

func TestCountFiles_emptyDir(t *testing.T) {
	dir := t.TempDir()
	if got := countFiles(dir); got != 0 {
		t.Errorf("countFiles(empty dir) = %d, want 0", got)
	}
}

func TestCountFiles_withFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.md", "b.yaml", "sub/c.md"} {
		path := dir + "/" + name
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := countFiles(dir); got != 3 {
		t.Errorf("countFiles = %d, want 3", got)
	}
}

func TestCountFiles_nonexistentDir(t *testing.T) {
	if got := countFiles("/definitely/does/not/exist"); got != 0 {
		t.Errorf("countFiles(nonexistent) = %d, want 0", got)
	}
}

// ── managedFilter ─────────────────────────────────────────────────────────────

func TestManagedFilter_CLAUDE_md_filtered(t *testing.T) {
	f := managedFilter(nil)
	if !f("CLAUDE.md") {
		t.Error("managedFilter: CLAUDE.md should be filtered (managed)")
	}
}

func TestManagedFilter_managed_subdir_filtered(t *testing.T) {
	srcs := []source.Source{{Structure: source.DefaultStructure()}}
	f := managedFilter(srcs)
	if !f("commands/foo.md") {
		t.Error("managedFilter: commands/foo.md should be filtered")
	}
}

func TestManagedFilter_unmanaged_not_filtered(t *testing.T) {
	f := managedFilter(nil)
	if f("custom/notes.md") {
		t.Error("managedFilter: custom/notes.md should NOT be filtered")
	}
}

// ── sourceAttribution ─────────────────────────────────────────────────────────

func TestSourceAttribution_emptyAttribution(t *testing.T) {
	result := sourceAttribution(nil, nil)
	if result != nil {
		t.Errorf("sourceAttribution(nil) = %v, want nil", result)
	}
}

func TestSourceAttribution_mapsIndicesToNames(t *testing.T) {
	srcs := []source.Source{
		{Name: "work"},
		{Name: "personal"},
	}
	attribution := map[string][]int{
		"CLAUDE.md": {0, 1},
	}
	result := sourceAttribution(attribution, srcs)
	if len(result["CLAUDE.md"]) != 2 {
		t.Fatalf("sourceAttribution: CLAUDE.md has %d entries, want 2", len(result["CLAUDE.md"]))
	}
	if result["CLAUDE.md"][0] != "work" || result["CLAUDE.md"][1] != "personal" {
		t.Errorf("sourceAttribution: CLAUDE.md = %v, want [work personal]", result["CLAUDE.md"])
	}
}
