package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/watch"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func writeFile(t *testing.T, path, content string) {
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

func newSource(name, root string) source.Source {
	return source.Source{Name: name, Root: root}
}

func newProfile(srcs []source.Source) *profile.Profile {
	names := make([]string, len(srcs))
	for i, s := range srcs {
		names[i] = s.Name
	}
	return &profile.Profile{
		Name:    "test",
		Sources: names,
		Overlay: profile.OverlayCascade,
	}
}

// ── writeBackSingleSource ─────────────────────────────────────────────────────

func TestWriteBackSingleSource_ExistingFile_WrittenToOwner(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()

	// Source A owns CLAUDE.md.
	writeFile(t, filepath.Join(srcARoot, "CLAUDE.md"), "original rules")
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), "harness edited rules")

	srcs := []source.Source{newSource("work", srcARoot)}
	p := newProfile(srcs)
	m := &manifest.Manifest{
		Files: map[string]string{"CLAUDE.md": "sha256:abc"},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "CLAUDE.md"}

	performed, err := writeBackSingleSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackSingleSource: %v", err)
	}
	if !performed {
		t.Fatal("expected performed=true, got false")
	}
	if got := readFile(t, filepath.Join(srcARoot, "CLAUDE.md")); got != "harness edited rules" {
		t.Errorf("source CLAUDE.md = %q, want %q", got, "harness edited rules")
	}
}

func TestWriteBackSingleSource_MultiSource_Skipped(t *testing.T) {
	targetRoot := t.TempDir()
	srcRoot := t.TempDir()
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), "merged content")

	srcs := []source.Source{newSource("work", srcRoot), newSource("personal", srcRoot)}
	p := newProfile(srcs)
	m := &manifest.Manifest{
		Files: map[string]string{"CLAUDE.md": "sha256:abc"},
		SourceFiles: map[string][]string{
			"CLAUDE.md": {"work", "personal"},
		},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "CLAUDE.md"}

	performed, err := writeBackSingleSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackSingleSource: %v", err)
	}
	if performed {
		t.Error("expected performed=false for multi-source file, got true")
	}
}

func TestWriteBackSingleSource_SecondSourceOwns(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()
	srcBRoot := t.TempDir()

	// Only source B has commands/deploy.md.
	writeFile(t, filepath.Join(srcBRoot, "commands", "deploy.md"), "original deploy")
	writeFile(t, filepath.Join(targetRoot, "commands", "deploy.md"), "edited deploy")

	srcs := []source.Source{newSource("work", srcARoot), newSource("personal", srcBRoot)}
	p := newProfile(srcs)
	m := &manifest.Manifest{
		Files: map[string]string{filepath.Join("commands", "deploy.md"): "sha256:abc"},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: filepath.Join("commands", "deploy.md")}

	performed, err := writeBackSingleSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackSingleSource: %v", err)
	}
	if !performed {
		t.Fatal("expected performed=true")
	}
	got := readFile(t, filepath.Join(srcBRoot, "commands", "deploy.md"))
	if got != "edited deploy" {
		t.Errorf("source file = %q, want %q", got, "edited deploy")
	}
	// Source A should NOT have been written to.
	if _, err := os.Stat(filepath.Join(srcARoot, "commands", "deploy.md")); err == nil {
		t.Error("source A should not have been written to")
	}
}

func TestWriteBackSingleSource_NewFile_UsesWriteBackDefault(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()
	writeFile(t, filepath.Join(targetRoot, "new-file.md"), "new content from harness")

	srcs := []source.Source{newSource("work", srcARoot)}
	p := &profile.Profile{
		Name:      "test",
		Sources:   []string{"work"},
		Overlay:   profile.OverlayCascade,
		WriteBack: profile.WriteBack{Default: "work"},
	}
	m := &manifest.Manifest{
		Files: map[string]string{"new-file.md": "sha256:abc"},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "new-file.md"}

	performed, err := writeBackSingleSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackSingleSource: %v", err)
	}
	if !performed {
		t.Fatal("expected performed=true when write_back.default is set")
	}
	got := readFile(t, filepath.Join(srcARoot, "new-file.md"))
	if got != "new content from harness" {
		t.Errorf("source file = %q, want %q", got, "new content from harness")
	}
}

func TestWriteBackSingleSource_NewFile_NoDefault_Skipped(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()
	writeFile(t, filepath.Join(targetRoot, "new-file.md"), "new content")

	srcs := []source.Source{newSource("work", srcARoot)}
	p := newProfile(srcs) // no write_back config
	m := &manifest.Manifest{
		Files: map[string]string{"new-file.md": "sha256:abc"},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "new-file.md"}

	performed, err := writeBackSingleSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackSingleSource: %v", err)
	}
	if performed {
		t.Error("expected performed=false when no source owns the file and no default is set")
	}
}

func TestWriteBackSingleSource_NewFile_UsesOverride(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()
	srcBRoot := t.TempDir()
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), "override content")

	srcs := []source.Source{newSource("work", srcARoot), newSource("personal", srcBRoot)}
	p := &profile.Profile{
		Name:    "test",
		Sources: []string{"work", "personal"},
		Overlay: profile.OverlayCascade,
		WriteBack: profile.WriteBack{
			Default:   "work",
			Overrides: map[string]string{"CLAUDE.md": "personal"},
		},
	}
	m := &manifest.Manifest{Files: map[string]string{"CLAUDE.md": "sha256:abc"}}
	c := watch.TargetChange{Root: targetRoot, Rel: "CLAUDE.md"}

	performed, err := writeBackSingleSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackSingleSource: %v", err)
	}
	if !performed {
		t.Fatal("expected performed=true")
	}
	// Override routes to "personal", not "work".
	got := readFile(t, filepath.Join(srcBRoot, "CLAUDE.md"))
	if got != "override content" {
		t.Errorf("personal source CLAUDE.md = %q, want %q", got, "override content")
	}
	if _, err := os.Stat(filepath.Join(srcARoot, "CLAUDE.md")); err == nil {
		t.Error("work source should not have been written to")
	}
}
