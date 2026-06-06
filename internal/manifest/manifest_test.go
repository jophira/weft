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
