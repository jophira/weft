package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── expandHome ────────────────────────────────────────────────────────────────

func TestExpandHome_tildePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := expandHome("~/.config/weft")
	want := filepath.Join(home, ".config", "weft")
	if got != want {
		t.Errorf("expandHome(~/.config/weft) = %q, want %q", got, want)
	}
}

func TestExpandHome_absolutePath(t *testing.T) {
	path := "/absolute/path"
	if got := expandHome(path); got != path {
		t.Errorf("expandHome(%q) = %q, want unchanged", path, got)
	}
}

func TestExpandHome_relPath(t *testing.T) {
	path := "relative/path"
	if got := expandHome(path); got != path {
		t.Errorf("expandHome(%q) = %q, want unchanged", path, got)
	}
}

// ── contractHome ──────────────────────────────────────────────────────────────

func TestContractHome_roundtrip(t *testing.T) {
	home, _ := os.UserHomeDir()
	original := filepath.Join(home, ".config", "weft", "memory.md")
	if got := contractHome(original); !strings.HasPrefix(got, "~/") {
		t.Errorf("contractHome(%q) = %q, expected ~/... form", original, got)
	}
}

func TestContractHome_outsideHome(t *testing.T) {
	path := "/etc/hosts"
	if got := contractHome(path); got != path {
		t.Errorf("contractHome(%q) = %q, want unchanged", path, got)
	}
}

// ── sourceRoot ────────────────────────────────────────────────────────────────

func TestSourceRoot_found(t *testing.T) {
	dir := t.TempDir()
	// Write a minimal source YAML.
	yaml := "name: test\nroot: /tmp/test-root\n"
	if err := os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Executor{sourcesDir: dir}
	root, err := e.sourceRoot("test")
	if err != nil {
		t.Fatalf("sourceRoot: %v", err)
	}
	if root != "/tmp/test-root" {
		t.Errorf("sourceRoot = %q, want /tmp/test-root", root)
	}
}

func TestSourceRoot_notFound(t *testing.T) {
	dir := t.TempDir()
	e := &Executor{sourcesDir: dir}
	if _, err := e.sourceRoot("nonexistent"); err == nil {
		t.Error("sourceRoot(nonexistent): expected error, got nil")
	}
}

func TestSourceRoot_missingRootField(t *testing.T) {
	dir := t.TempDir()
	yaml := "name: test\n" // no root field
	if err := os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Executor{sourcesDir: dir}
	if _, err := e.sourceRoot("test"); err == nil {
		t.Error("sourceRoot with empty root: expected error, got nil")
	}
}

// ── appendMemory ──────────────────────────────────────────────────────────────

func TestAppendMemory_writesEntry(t *testing.T) {
	dir := t.TempDir()
	srcRoot := t.TempDir()

	// Write source YAML pointing to srcRoot.
	yaml := "name: mysrc\nroot: " + srcRoot + "\n"
	if err := os.WriteFile(filepath.Join(dir, "mysrc.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	e := &Executor{sourcesDir: dir}
	h := Hook{
		Name:    "test",
		Trigger: TriggerManual,
		Prompt:  "remember this",
		Action: Action{
			Type:         ActionAppendMemory,
			TargetSource: "mysrc",
			SummaryTo:    "memory/project.md",
		},
	}
	if err := e.appendMemory(h); err != nil {
		t.Fatalf("appendMemory: %v", err)
	}

	// Verify the file was written with the prompt text.
	data, err := os.ReadFile(filepath.Join(srcRoot, "memory", "project.md"))
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if !strings.Contains(string(data), "remember this") {
		t.Errorf("written file does not contain prompt text: %q", data)
	}
}

func TestAppendMemory_emptyPromptReturnsError(t *testing.T) {
	dir := t.TempDir()
	e := &Executor{sourcesDir: dir}
	h := Hook{
		Name: "test", Trigger: TriggerManual,
		Prompt: "  ",
		Action: Action{Type: ActionAppendMemory, TargetSource: "src", SummaryTo: "memory/x.md"},
	}
	if err := e.appendMemory(h); err == nil {
		t.Error("appendMemory with empty prompt: expected error, got nil")
	}
}
