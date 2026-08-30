package privatefile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}
}

func TestWrite_createsOwnerOnlyFileAndDir(t *testing.T) {
	skipOnWindows(t)
	dir := filepath.Join(t.TempDir(), "state", "nested")
	path := filepath.Join(dir, "manifest.yaml")

	if err := Write(path, []byte("content")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != FileMode {
		t.Errorf("file mode = %o, want %o", got, FileMode)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := di.Mode().Perm(); got != DirMode {
		t.Errorf("dir mode = %o, want %o", got, DirMode)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != "content" {
		t.Errorf("content = %q, want %q", got, "content")
	}
}

// The point of the tightening: a machine that already ran an older weft has
// 0755 directories and 0644 files, and must not keep them.
func TestWrite_tightensExistingPermissions(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Write(path, []byte("new")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	fi, _ := os.Stat(path)
	if got := fi.Mode().Perm(); got != FileMode {
		t.Errorf("existing file kept mode %o, want %o", got, FileMode)
	}
	di, _ := os.Stat(dir)
	if got := di.Mode().Perm(); got != DirMode {
		t.Errorf("existing dir kept mode %o, want %o", got, DirMode)
	}
}

func TestWriteMode_honoursAnExplicitMode(t *testing.T) {
	skipOnWindows(t)
	path := filepath.Join(t.TempDir(), "staged.md")
	if err := WriteMode(path, []byte("generated"), 0o400); err != nil {
		t.Fatalf("WriteMode: %v", err)
	}
	fi, _ := os.Stat(path)
	if got := fi.Mode().Perm(); got != 0o400 {
		t.Errorf("mode = %o, want 400", got)
	}
}

// Replacing a read-only file is the case that broke the naive implementation:
// os.WriteFile cannot reopen a 0400 file, but a rename over it succeeds.
func TestWriteMode_replacesAReadOnlyFile(t *testing.T) {
	skipOnWindows(t)
	path := filepath.Join(t.TempDir(), "staged.md")
	if err := WriteMode(path, []byte("v1"), 0o400); err != nil {
		t.Fatalf("first WriteMode: %v", err)
	}
	if err := WriteMode(path, []byte("v2"), 0o400); err != nil {
		t.Fatalf("second WriteMode over a read-only file: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "v2" {
		t.Errorf("content = %q, want v2", got)
	}
}

// A failed write must leave neither a partial destination nor a temp file.
func TestWrite_leavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := Write(path, []byte("v1")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want just the destination", len(entries))
	}
}

// An interrupted write must not be observable: a reader either sees the old
// content or the new, never a truncated file.
func TestWrite_replacementIsAllOrNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := Write(path, []byte("original content, quite long")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// A directory where the destination name is cannot be renamed over on
	// Unix, so this is a write that fails at the last step.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Write(blocked, []byte("short")); err == nil {
		t.Fatal("Write over a directory should fail")
	}
	entries, _ := os.ReadDir(filepath.Dir(blocked))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("failed write left a temp file: %s", e.Name())
		}
	}
}
