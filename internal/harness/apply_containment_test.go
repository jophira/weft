package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// symlinkOrSkip plants a symlink, skipping the test where the platform will not
// allow one (unprivileged Windows).
func symlinkOrSkip(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatalf("planting symlink: %v", err)
	}
}

// A symlinked directory inside the harness tree must not redirect a projection.
// Before the fix, os.WriteFile followed the link and weft wrote the user's rules
// into whatever the link pointed at (#278).
func TestApply_SymlinkedDirectoryCannotRedirectAWrite(t *testing.T) {
	f := newApplyFixture(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "victim.md"), []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, filepath.Join(f.target, "commands"))

	staged := t.TempDir()
	write(t, filepath.Join(staged, "commands", "victim.md"), "projected content")
	h := &GenericHarness{detection: resolved(f.target), name: "test-harness"}
	err := h.Apply(staged, f.ctx)

	// Either the apply refuses outright or it writes inside the root. What it
	// must never do is change the file on the far side of the link.
	got, readErr := os.ReadFile(filepath.Join(outside, "victim.md"))
	if readErr != nil {
		t.Fatalf("reading the file outside the root: %v", readErr)
	}
	if string(got) != "do not touch" {
		t.Errorf("the write escaped the harness root through a symlink (apply err: %v)", err)
	}
}

// The same for the final path component: a symlinked file must not become a
// write to its target.
func TestApply_SymlinkedFileCannotRedirectAWrite(t *testing.T) {
	f := newApplyFixture(t)
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.md")
	if err := os.WriteFile(victim, []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, victim, filepath.Join(f.target, "CLAUDE.md"))

	staged := t.TempDir()
	write(t, filepath.Join(staged, "CLAUDE.md"), "projected content")
	h := &GenericHarness{detection: resolved(f.target), name: "test-harness"}
	err := h.Apply(staged, f.ctx)

	got, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatalf("reading the file outside the root: %v", readErr)
	}
	if string(got) != "do not touch" {
		t.Errorf("the write escaped the harness root through a symlinked file (apply err: %v)", err)
	}
}

// Pruning is a delete, so a symlink redirecting it is worse than a redirected
// write. The manifest entry names a file weft believes it owns; the link makes
// that name resolve somewhere else.
func TestPruneDropped_SymlinkCannotRedirectADelete(t *testing.T) {
	f := newApplyFixture(t)
	f.apply(t, map[string]string{"commands/gone.md": "v1"})

	// Replace the projected file with a link to something outside the root,
	// and tell the manifest the link's content is what weft wrote — so prune
	// believes the file is unmodified and deletable.
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.md")
	if err := os.WriteFile(victim, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	projected := filepath.Join(f.target, "commands", "gone.md")
	if err := os.Remove(projected); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, victim, projected)

	// Re-apply without the file: it is now a dropped path, so prune runs on it.
	f.apply(t, map[string]string{"CLAUDE.md": "unrelated"})

	if _, err := os.Stat(victim); err != nil {
		t.Errorf("prune deleted a file outside the harness root through a symlink: %v", err)
	}
}

// pruneEmptyDirs walks upward. It must stop at the root rather than climbing out
// of it, which the relative "." terminator now guarantees by construction.
func TestPruneDropped_EmptyDirWalkStopsAtTheRoot(t *testing.T) {
	f := newApplyFixture(t)
	parent := filepath.Dir(f.target)
	f.apply(t, map[string]string{"skills/lint/SKILL.md": "v1"})
	f.apply(t, map[string]string{"CLAUDE.md": "v1"})

	if f.exists(filepath.Join("skills", "lint")) {
		t.Error("the emptied skill directory should have been pruned")
	}
	if _, err := os.Stat(f.target); err != nil {
		t.Errorf("the harness root itself was pruned: %v", err)
	}
	if _, err := os.Stat(parent); err != nil {
		t.Errorf("the walk climbed above the harness root: %v", err)
	}
}

// A conflict backup is a write driven by a manifest-derived relative path, so it
// gets the same containment as the projection it protects.
func TestBackupConflicts_StaysUnderTheBackupDir(t *testing.T) {
	cfgDir := t.TempDir()
	src := t.TempDir()
	abs := filepath.Join(src, "real.md")
	if err := os.WriteFile(abs, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir, err := backupConflicts([]conflictFile{{rel: filepath.Join("..", "..", "escaped.md"), abs: abs}}, "test-harness", cfgDir)
	if err == nil {
		t.Fatalf("backupConflicts accepted an escaping rel, writing under %s", dir)
	}
	escaped := filepath.Join(filepath.Dir(cfgDir), "escaped.md")
	if _, sErr := os.Stat(escaped); sErr == nil {
		t.Errorf("the backup was written outside the config dir at %s", escaped)
	}
}

// The ordinary backup path must still work — containment that breaks the
// feature it guards is not a fix.
func TestBackupConflicts_NestedRelStillWorks(t *testing.T) {
	cfgDir := t.TempDir()
	src := t.TempDir()
	abs := filepath.Join(src, "real.md")
	if err := os.WriteFile(abs, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	rel := filepath.Join("commands", "nested", "file.md")
	dir, err := backupConflicts([]conflictFile{{rel: rel, abs: abs}}, "test-harness", cfgDir)
	if err != nil {
		t.Fatalf("backupConflicts: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("reading the backup: %v", err)
	}
	if string(got) != "content" {
		t.Errorf("backup content = %q, want %q", got, "content")
	}
}
