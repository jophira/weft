package homemove

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestMove_relocatesAndBridges(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "old", "sources")
	dst := filepath.Join(base, "weft", "sources")
	writeFile(t, filepath.Join(src, "a.md"), "hello")

	res, err := Move(src, dst, true)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if !res.Moved || !res.Bridged {
		t.Fatalf("Result = %+v, want Moved && Bridged", res)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "a.md")); string(got) != "hello" {
		t.Errorf("content not moved to dst: %q", got)
	}
	// The bridge symlink resolves the old path to the new content.
	if got, _ := os.ReadFile(filepath.Join(src, "a.md")); string(got) != "hello" {
		t.Errorf("bridge symlink does not resolve: %q", got)
	}
}

func TestMove_idempotentSecondRun(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "old")
	dst := filepath.Join(base, "new")
	writeFile(t, filepath.Join(src, "a.md"), "x")

	if _, err := Move(src, dst, true); err != nil {
		t.Fatalf("first Move: %v", err)
	}
	res, err := Move(src, dst, true)
	if err != nil {
		t.Fatalf("second Move: %v", err)
	}
	if res.Moved {
		t.Errorf("second run should be a no-op, got %+v", res)
	}
	if res.SkipReason == "" {
		t.Errorf("expected a SkipReason on idempotent re-run")
	}
}

func TestMove_absentSourceIsNoOp(t *testing.T) {
	base := t.TempDir()
	res, err := Move(filepath.Join(base, "missing"), filepath.Join(base, "dst"), true)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if res.Moved || res.SkipReason == "" {
		t.Errorf("absent source should be a no-op, got %+v", res)
	}
}

func TestMove_refusesToClobberPopulatedDest(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "old")
	dst := filepath.Join(base, "new")
	writeFile(t, filepath.Join(src, "a.md"), "src")
	writeFile(t, filepath.Join(dst, "b.md"), "dst")

	_, err := Move(src, dst, false)
	if !errors.Is(err, ErrDestPopulated) {
		t.Fatalf("expected ErrDestPopulated, got %v", err)
	}
	// Both sides untouched.
	if got, _ := os.ReadFile(filepath.Join(src, "a.md")); string(got) != "src" {
		t.Errorf("src was disturbed: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "b.md")); string(got) != "dst" {
		t.Errorf("dst was disturbed: %q", got)
	}
}

func TestMove_emptyDestIsNotClobber(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "old")
	dst := filepath.Join(base, "new")
	writeFile(t, filepath.Join(src, "a.md"), "src")
	if err := os.MkdirAll(dst, 0o755); err != nil { // empty dst dir pre-exists
		t.Fatalf("mkdir dst: %v", err)
	}
	res, err := Move(src, dst, false)
	if err != nil {
		t.Fatalf("Move into empty dst: %v", err)
	}
	if !res.Moved {
		t.Errorf("expected move into empty dst, got %+v", res)
	}
}

func TestMove_samePathNoOp(t *testing.T) {
	base := t.TempDir()
	p := filepath.Join(base, "x")
	writeFile(t, filepath.Join(p, "a.md"), "x")
	res, err := Move(p, p, true)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if res.Moved {
		t.Errorf("same-path move should be a no-op, got %+v", res)
	}
}
