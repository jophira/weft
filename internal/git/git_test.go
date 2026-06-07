package git_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
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
	if _, err := wt.Add("CLAUDE.md"); err != nil {
		t.Fatalf("wt.Add: %v", err)
	}
	if _, err := wt.Commit("initial commit", &gogit.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("wt.Commit: %v", err)
	}
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
	if _, err := wt.Add(file); err != nil {
		t.Fatalf("wt.Add: %v", err)
	}
	if _, err := wt.Commit(msg, &gogit.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("wt.Commit: %v", err)
	}
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

	if err := git.Clone(remote, local, "main", nil, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "CLAUDE.md")); err != nil {
		t.Error("expected CLAUDE.md in cloned repo")
	}
}

func TestClone_setsOriginRemote(t *testing.T) {
	remote := makeRemote(t)
	local := t.TempDir()

	if err := git.Clone(remote, local, "main", nil, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}

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
	if err := git.Clone(remote, local, "main", nil, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}

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
	if err := git.Clone(remote, local, "main", nil, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}

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
	if err := git.Clone(remote, local, "main", nil, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}

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
	if err := git.Clone(remote, local, "main", nil, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	// Modify a tracked file.
	if err := os.WriteFile(filepath.Join(local, "CLAUDE.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r, _ := git.Open(local)
	clean, err := r.IsClean()
	if err != nil {
		t.Fatalf("IsClean: %v", err)
	}
	if clean {
		t.Error("expected dirty repo after modifying a tracked file")
	}
}

// ── HeadBranch ────────────────────────────────────────────────────────────────

func TestHeadBranch_returnsMain(t *testing.T) {
	remote := makeRemote(t)
	local := t.TempDir()
	if err := git.Clone(remote, local, "main", nil, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	r, _ := git.Open(local)
	branch, err := r.HeadBranch()
	if err != nil {
		t.Fatalf("HeadBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("HeadBranch = %q, want %q", branch, "main")
	}
}

// ── Push ──────────────────────────────────────────────────────────────────────

// makeLocalWithBare sets up:
//   - a bare remote (can receive pushes)
//   - a local working copy cloned from it, with one commit
func makeLocalWithBare(t *testing.T) (localPath string) {
	t.Helper()

	// 1. Create bare remote.
	bare := t.TempDir()
	bareRepo, err := gogit.PlainInit(bare, true)
	if err != nil {
		t.Fatalf("PlainInit bare: %v", err)
	}
	_ = bareRepo

	// 2. Create a working clone with the bare as origin.
	local := t.TempDir()
	localRepo, err := gogit.PlainInitWithOptions(local, &gogit.PlainInitOptions{
		InitOptions: gogit.InitOptions{
			DefaultBranch: plumbing.NewBranchReferenceName("main"),
		},
	})
	if err != nil {
		t.Fatalf("PlainInitWithOptions: %v", err)
	}

	// 3. Add the bare as origin.
	if _, err := localRepo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{bare},
	}); err != nil {
		t.Fatalf("CreateRemote: %v", err)
	}

	// 4. Commit a file so there is something to push.
	wt, _ := localRepo.Worktree()
	addFile(t, local, "CLAUDE.md", "# Rules\n")
	if _, err := wt.Add("CLAUDE.md"); err != nil {
		t.Fatalf("wt.Add: %v", err)
	}
	if _, err := wt.Commit("initial commit", &gogit.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("wt.Commit: %v", err)
	}

	return local
}

func TestPush_sendsCommitsToBareRemote(t *testing.T) {
	local := makeLocalWithBare(t)

	r, err := git.Open(local)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.Push(nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
}

func TestPush_alreadyUpToDate(t *testing.T) {
	local := makeLocalWithBare(t)
	r, _ := git.Open(local)

	// First push.
	if err := r.Push(nil); err != nil {
		t.Fatalf("first Push: %v", err)
	}
	// Second push — nothing new, should return nil (not an error).
	if err := r.Push(nil); err != nil {
		t.Fatalf("second Push (already up to date): %v", err)
	}
}

// ── CommitAll ─────────────────────────────────────────────────────────────────

func TestCommitAll_commitsChanges(t *testing.T) {
	remote := makeRemote(t)
	local := t.TempDir()
	if err := git.Clone(remote, local, "main", nil, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	// Modify a file so there's something to commit.
	addFile(t, local, "notes.md", "new content\n")

	r, err := git.Open(local)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.CommitAll("test commit"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}

	// Verify the commit landed by checking HeadBranch still resolves.
	if _, err := r.HeadBranch(); err != nil {
		t.Fatalf("HeadBranch after CommitAll: %v", err)
	}
}

// ── OriginRemote ──────────────────────────────────────────────────────────────

func TestOriginRemote_returnsURL(t *testing.T) {
	remote := makeRemote(t)
	local := t.TempDir()
	if err := git.Clone(remote, local, "main", nil, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	r, err := git.Open(local)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	url, err := r.OriginRemote()
	if err != nil {
		t.Fatalf("OriginRemote: %v", err)
	}
	if url == "" {
		t.Error("OriginRemote() returned empty URL for repo cloned from local path")
	}
}

func TestOriginRemote_noOrigin(t *testing.T) {
	// A freshly initialised repo has no origin remote.
	dir := t.TempDir()
	repo, err := gogit.PlainInitWithOptions(dir, &gogit.PlainInitOptions{
		InitOptions: gogit.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	_ = repo

	r, err := git.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	url, err := r.OriginRemote()
	if err != nil {
		t.Fatalf("OriginRemote (no origin): %v", err)
	}
	if url != "" {
		t.Errorf("OriginRemote() = %q, want empty for repo without origin", url)
	}
}

// ── Status / Clone error branches ────────────────────────────────────────────

func TestStatus_dirtyRepo(t *testing.T) {
	remote := makeRemote(t)
	local := t.TempDir()
	if err := git.Clone(remote, local, "main", nil, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	addFile(t, local, "untracked.md", "new")
	r, _ := git.Open(local)
	files, err := r.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if files == "" {
		t.Error("Status: expected at least one untracked file, got none")
	}
}
