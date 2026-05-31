package git_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/jophira/weft/internal/git"
)

// makeRemote creates a local git repository with one commit and returns its path.
// Using a local path as the "remote" avoids any network or auth dependency.
func makeRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	repo, err := gogit.PlainInitWithOptions(dir, &gogit.PlainInitOptions{
		InitOptions: gogit.InitOptions{
			DefaultBranch: plumbing.NewBranchReferenceName("main"),
		},
	})
	if err != nil {
		t.Fatalf("PlainInitWithOptions: %v", err)
	}
	wt, _ := repo.Worktree()

	addFile(t, dir, "CLAUDE.md", "# Rules\n")
	wt.Add("CLAUDE.md")
	wt.Commit("initial commit", &gogit.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})
	return dir
}

// addCommit adds a file and commits it to an existing local repo.
func addCommit(t *testing.T, repoPath, file, content, msg string) {
	t.Helper()
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	wt, _ := repo.Worktree()
	addFile(t, repoPath, file, content)
	wt.Add(file)
	wt.Commit(msg, &gogit.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})
}

func addFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ── Clone ─────────────────────────────────────────────────────────────────────

func TestClone_createsLocalCopy(t *testing.T) {
	remote := makeRemote(t)
	local := t.TempDir()

	if err := git.Clone(remote, local, "main", nil); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "CLAUDE.md")); err != nil {
		t.Error("expected CLAUDE.md in cloned repo")
	}
}

func TestClone_setsOriginRemote(t *testing.T) {
	remote := makeRemote(t)
	local := t.TempDir()

	git.Clone(remote, local, "main", nil)

	repo, err := gogit.PlainOpen(local)
	if err != nil {
		t.Fatalf("PlainOpen after clone: %v", err)
	}
	if _, err := repo.Remote("origin"); err != nil {
		t.Error("expected origin remote to be set after clone")
	}
}

// ── Pull ──────────────────────────────────────────────────────────────────────

func TestPull_alreadyUpToDate(t *testing.T) {
	remote := makeRemote(t)
	local := t.TempDir()
	git.Clone(remote, local, "main", nil)

	r, err := git.Open(local)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	updated, err := r.Pull("main", nil)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if updated {
		t.Error("expected updated=false when already up to date")
	}
}

func TestPull_fetchesNewCommit(t *testing.T) {
	remote := makeRemote(t)
	local := t.TempDir()
	git.Clone(remote, local, "main", nil)

	// Push a new commit to the remote after the clone.
	addCommit(t, remote, "update.md", "new content\n", "add update")

	r, _ := git.Open(local)
	updated, err := r.Pull("main", nil)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if !updated {
		t.Error("expected updated=true after new commit in remote")
	}
	// Verify the new file was pulled.
	if _, err := os.Stat(filepath.Join(local, "update.md")); err != nil {
		t.Error("expected update.md in local repo after pull")
	}
}

// ── IsRepo ────────────────────────────────────────────────────────────────────

func TestIsRepo_true(t *testing.T) {
	remote := makeRemote(t)
	if !git.IsRepo(remote) {
		t.Error("expected IsRepo=true for a git repository")
	}
}

func TestIsRepo_false(t *testing.T) {
	plain := t.TempDir() // just a plain directory
	if git.IsRepo(plain) {
		t.Error("expected IsRepo=false for a plain directory")
	}
}

func TestIsRepo_nonExistent(t *testing.T) {
	if git.IsRepo("/tmp/this-path-does-not-exist-weft-test") {
		t.Error("expected IsRepo=false for nonexistent path")
	}
}

// ── Open ──────────────────────────────────────────────────────────────────────

func TestOpen_notARepo(t *testing.T) {
	if _, err := git.Open(t.TempDir()); err == nil {
		t.Fatal("expected error opening a non-repo directory")
	}
}

// ── IsClean ───────────────────────────────────────────────────────────────────

func TestIsClean_freshCloneIsClean(t *testing.T) {
	remote := makeRemote(t)
	local := t.TempDir()
	git.Clone(remote, local, "main", nil)

	r, _ := git.Open(local)
	clean, err := r.IsClean()
	if err != nil {
		t.Fatalf("IsClean: %v", err)
	}
	if !clean {
		t.Error("expected fresh clone to be clean")
	}
}

func TestIsClean_modifiedFileIsDirty(t *testing.T) {
	remote := makeRemote(t)
	local := t.TempDir()
	git.Clone(remote, local, "main", nil)

	// Modify a tracked file.
	os.WriteFile(filepath.Join(local, "CLAUDE.md"), []byte("changed\n"), 0o644)

	r, _ := git.Open(local)
	clean, err := r.IsClean()
	if err != nil {
		t.Fatalf("IsClean: %v", err)
	}
	if clean {
		t.Error("expected dirty repo after modifying a tracked file")
	}
}
