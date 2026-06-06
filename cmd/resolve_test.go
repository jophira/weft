package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/manifest"
)

// ── expandAndAbs ──────────────────────────────────────────────────────────────

func TestExpandAndAbs_HomeTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	got, err := expandAndAbs("~/foo/bar")
	if err != nil {
		t.Fatalf("expandAndAbs: %v", err)
	}
	want := filepath.Join(home, "foo", "bar")
	if got != want {
		t.Errorf("expandAndAbs(~/foo/bar) = %q, want %q", got, want)
	}
}

func TestExpandAndAbs_AbsolutePassThrough(t *testing.T) {
	got, err := expandAndAbs("/tmp/foo")
	if err != nil {
		t.Fatalf("expandAndAbs: %v", err)
	}
	if got != "/tmp/foo" {
		t.Errorf("expandAndAbs(/tmp/foo) = %q, want %q", got, "/tmp/foo")
	}
}

// ── findManifest ──────────────────────────────────────────────────────────────

func writeManifest(t *testing.T, cfgDir string, m *manifest.Manifest) {
	t.Helper()
	if err := manifest.Save(cfgDir, m); err != nil {
		t.Fatalf("saving manifest: %v", err)
	}
}

func TestFindManifest_MatchesOwned(t *testing.T) {
	cfgDir := t.TempDir()
	targetRoot := t.TempDir()

	m := &manifest.Manifest{
		Harness:    "claude-code",
		Profile:    "hybrid",
		TargetRoot: targetRoot,
		Files:      map[string]string{"CLAUDE.md": "sha256:abc"},
	}
	writeManifest(t, cfgDir, m)

	targetFile := filepath.Join(targetRoot, "CLAUDE.md")
	got, rel, err := findManifest(cfgDir, targetFile)
	if err != nil {
		t.Fatalf("findManifest: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil manifest, got nil")
	}
	if got.Harness != "claude-code" {
		t.Errorf("Harness = %q, want claude-code", got.Harness)
	}
	if rel != "CLAUDE.md" {
		t.Errorf("rel = %q, want CLAUDE.md", rel)
	}
}

func TestFindManifest_FileNotInManifest(t *testing.T) {
	cfgDir := t.TempDir()
	targetRoot := t.TempDir()

	m := &manifest.Manifest{
		Harness:    "claude-code",
		TargetRoot: targetRoot,
		Files:      map[string]string{"CLAUDE.md": "sha256:abc"},
	}
	writeManifest(t, cfgDir, m)

	// File is under targetRoot but NOT listed in Files.
	targetFile := filepath.Join(targetRoot, "unknown.md")
	got, _, err := findManifest(cfgDir, targetFile)
	if err != nil {
		t.Fatalf("findManifest: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil manifest for untracked file, got %+v", got)
	}
}

func TestFindManifest_FileOutsideAllRoots(t *testing.T) {
	cfgDir := t.TempDir()
	targetRoot := t.TempDir()

	m := &manifest.Manifest{
		Harness:    "claude-code",
		TargetRoot: targetRoot,
		Files:      map[string]string{"CLAUDE.md": "sha256:abc"},
	}
	writeManifest(t, cfgDir, m)

	// Completely unrelated path.
	got, _, err := findManifest(cfgDir, "/tmp/unrelated/file.md")
	if err != nil {
		t.Fatalf("findManifest: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil manifest for unrelated path, got %+v", got)
	}
}

func TestFindManifest_NoManifestsDir(t *testing.T) {
	cfgDir := t.TempDir() // no manifests/ subdir

	got, _, err := findManifest(cfgDir, "/tmp/any.md")
	if err != nil {
		t.Fatalf("findManifest: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil when no manifests dir, got %+v", got)
	}
}

func TestFindManifest_SubdirFile(t *testing.T) {
	cfgDir := t.TempDir()
	targetRoot := t.TempDir()

	m := &manifest.Manifest{
		Harness:    "claude-code",
		Profile:    "hybrid",
		TargetRoot: targetRoot,
		Files: map[string]string{
			filepath.Join("commands", "deploy.md"): "sha256:def",
		},
	}
	writeManifest(t, cfgDir, m)

	targetFile := filepath.Join(targetRoot, "commands", "deploy.md")
	got, rel, err := findManifest(cfgDir, targetFile)
	if err != nil {
		t.Fatalf("findManifest: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil manifest for subdir file")
	}
	wantRel := filepath.Join("commands", "deploy.md")
	if rel != wantRel {
		t.Errorf("rel = %q, want %q", rel, wantRel)
	}
}

func TestFindManifest_SourceFilesReturned(t *testing.T) {
	cfgDir := t.TempDir()
	targetRoot := t.TempDir()

	m := &manifest.Manifest{
		Harness:    "claude-code",
		Profile:    "hybrid",
		TargetRoot: targetRoot,
		Files:      map[string]string{"CLAUDE.md": "sha256:abc"},
		SourceFiles: map[string][]string{
			"CLAUDE.md": {"work", "personal"},
		},
	}
	writeManifest(t, cfgDir, m)

	targetFile := filepath.Join(targetRoot, "CLAUDE.md")
	got, _, err := findManifest(cfgDir, targetFile)
	if err != nil {
		t.Fatalf("findManifest: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil manifest")
	}
	srcs := got.SourceFiles["CLAUDE.md"]
	if len(srcs) != 2 || srcs[0] != "work" || srcs[1] != "personal" {
		t.Errorf("SourceFiles[CLAUDE.md] = %v, want [work personal]", srcs)
	}
}
