package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkPlaneBundle_absentKBIsEmpty(t *testing.T) {
	withIsolatedConfig(t) // weft_home == base
	repo := filepath.Join(t.TempDir(), "myrepo")
	got, err := workPlaneBundle(repo)
	if err != nil {
		t.Fatalf("workPlaneBundle: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty bundle for repo with no KB, got %q", got)
	}
}

func TestWorkPlaneBundle_concatsKBSortedWithHeader(t *testing.T) {
	base := withIsolatedConfig(t) // weft_home == base
	repo := filepath.Join(t.TempDir(), "myrepo")
	kb := filepath.Join(base, "work", "projects", "myrepo", "kb")
	if err := os.MkdirAll(filepath.Join(kb, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir kb: %v", err)
	}
	// Deliberately out of lexical order to prove sorting.
	writeFileT(t, filepath.Join(kb, "b.md"), "second")
	writeFileT(t, filepath.Join(kb, "a.md"), "first")
	writeFileT(t, filepath.Join(kb, "sub", "c.md"), "third")

	got, err := workPlaneBundle(repo)
	if err != nil {
		t.Fatalf("workPlaneBundle: %v", err)
	}
	if !strings.HasPrefix(got, "# Project knowledge: myrepo") {
		t.Errorf("missing header, got:\n%s", got)
	}
	ai, bi, ci := strings.Index(got, "first"), strings.Index(got, "second"), strings.Index(got, "third")
	if ai >= bi || bi >= ci {
		t.Errorf("KB files not in sorted order (a<b<sub/c): a=%d b=%d c=%d\n%s", ai, bi, ci, got)
	}
}

func TestWorkPlaneBundle_expandsHomeAnchor(t *testing.T) {
	base := withIsolatedConfig(t)
	repo := filepath.Join(t.TempDir(), "myrepo")
	kb := filepath.Join(base, "work", "projects", "myrepo", "kb")
	if err := os.MkdirAll(kb, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFileT(t, filepath.Join(kb, "n.md"), "see {{weft.home}}/work")

	got, err := workPlaneBundle(repo)
	if err != nil {
		t.Fatalf("workPlaneBundle: %v", err)
	}
	want := "see " + base + "/work"
	if !strings.Contains(got, want) {
		t.Errorf("home anchor not expanded; want %q in:\n%s", want, got)
	}
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
