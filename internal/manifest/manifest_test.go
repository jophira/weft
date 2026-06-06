package manifest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_NilWhenAbsent(t *testing.T) {
	cfgDir := t.TempDir()
	m, err := Load(cfgDir, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("expected non-nil manifest")
	}
	if len(m.Files) != 0 {
		t.Errorf("expected empty Files map, got %v", m.Files)
	}
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	cfgDir := t.TempDir()
	m := &Manifest{
		Harness:    "claude-code",
		Profile:    "work",
		TargetRoot: "/home/user/.claude",
		AppliedAt:  time.Now().UTC().Truncate(time.Second),
		Files: map[string]string{
			"CLAUDE.md":          "sha256:abc",
			"commands/foo.md":    "sha256:def",
			"backend/java/rules": "sha256:ghi",
		},
	}
	if err := Save(cfgDir, m); err != nil {
		t.Fatal(err)
	}

	got, err := Load(cfgDir, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != m.Harness {
		t.Errorf("Harness = %q, want %q", got.Harness, m.Harness)
	}
	if got.Profile != m.Profile {
		t.Errorf("Profile = %q, want %q", got.Profile, m.Profile)
	}
	if got.TargetRoot != m.TargetRoot {
		t.Errorf("TargetRoot = %q, want %q", got.TargetRoot, m.TargetRoot)
	}
	for rel, want := range m.Files {
		if got.Files[rel] != want {
			t.Errorf("Files[%q] = %q, want %q", rel, got.Files[rel], want)
		}
	}
}

func TestSave_CreatesManifestsDir(t *testing.T) {
	cfgDir := t.TempDir()
	m := &Manifest{Harness: "cursor", Files: map[string]string{"weft.mdc": "sha256:x"}}
	if err := Save(cfgDir, m); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "manifests", "cursor.json")); err != nil {
		t.Errorf("manifest file not created: %v", err)
	}
}

func TestHashBytes_Deterministic(t *testing.T) {
	data := []byte("hello world")
	h1 := HashBytes(data)
	h2 := HashBytes(data)
	if h1 != h2 {
		t.Errorf("HashBytes not deterministic: %q vs %q", h1, h2)
	}
	if h1 == "" {
		t.Error("expected non-empty hash")
	}
}

func TestHashBytes_DifferentContent(t *testing.T) {
	if HashBytes([]byte("a")) == HashBytes([]byte("b")) {
		t.Error("different content produced same hash")
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	content := []byte("# Test\nsome content")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := HashBytes(content)
	if got != want {
		t.Errorf("HashFile = %q, want %q", got, want)
	}
}

func TestHashFile_MissingFile(t *testing.T) {
	_, err := HashFile("/nonexistent/path/file.md")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// ── SourceFiles ───────────────────────────────────────────────────────────────

func TestSaveAndLoad_SourceFilesRoundTrip(t *testing.T) {
	cfgDir := t.TempDir()
	m := &Manifest{
		Harness:    "claude-code",
		Profile:    "hybrid",
		TargetRoot: "/home/user/.claude",
		AppliedAt:  time.Now().UTC().Truncate(time.Second),
		Files: map[string]string{
			"CLAUDE.md": "sha256:abc",
		},
		SourceFiles: map[string][]string{
			"CLAUDE.md": {"work", "personal"},
		},
	}
	if err := Save(cfgDir, m); err != nil {
		t.Fatal(err)
	}
	got, err := Load(cfgDir, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	srcs, ok := got.SourceFiles["CLAUDE.md"]
	if !ok {
		t.Fatal("SourceFiles[CLAUDE.md] missing after round-trip")
	}
	if len(srcs) != 2 || srcs[0] != "work" || srcs[1] != "personal" {
		t.Errorf("SourceFiles[CLAUDE.md] = %v, want [work personal]", srcs)
	}
}

func TestLoad_OldManifestWithoutSourceFiles_LoadsClean(t *testing.T) {
	cfgDir := t.TempDir()
	// Write a manifest JSON that pre-dates the source_files field.
	raw := `{"harness":"claude-code","profile":"work","target_root":"/home/user/.claude","applied_at":"2024-01-01T00:00:00Z","files":{"CLAUDE.md":"sha256:abc"}}`
	p := filepath.Join(cfgDir, "manifests", "claude-code.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(cfgDir, "claude-code")
	if err != nil {
		t.Fatalf("Load failed on old manifest: %v", err)
	}
	if got.SourceFiles != nil {
		t.Errorf("expected nil SourceFiles for old manifest, got %v", got.SourceFiles)
	}
	if got.Files["CLAUDE.md"] != "sha256:abc" {
		t.Errorf("Files[CLAUDE.md] = %q, want sha256:abc", got.Files["CLAUDE.md"])
	}
}

func TestLoad_IsolatedByHarnessName(t *testing.T) {
	cfgDir := t.TempDir()

	ma := &Manifest{Harness: "claude-code", Profile: "work", Files: map[string]string{"CLAUDE.md": "sha256:aaa"}}
	mb := &Manifest{Harness: "codex", Profile: "work", Files: map[string]string{"AGENTS.md": "sha256:bbb"}}

	if err := Save(cfgDir, ma); err != nil {
		t.Fatal(err)
	}
	if err := Save(cfgDir, mb); err != nil {
		t.Fatal(err)
	}

	got, err := Load(cfgDir, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if got.Files["CLAUDE.md"] != "sha256:aaa" {
		t.Errorf("wrong file entry for claude-code manifest")
	}
	if _, ok := got.Files["AGENTS.md"]; ok {
		t.Error("claude-code manifest should not contain codex entries")
	}
}
