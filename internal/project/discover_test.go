package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo makes a plain checkout with an origin remote.
func newRepo(t *testing.T, remote string) string {
	t.Helper()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	cfg := "[core]\n\trepositoryformatversion = 0\n"
	if remote != "" {
		cfg += "[remote \"origin\"]\n\turl = " + remote + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

// newWorktree makes a worktree of main, i.e. a directory whose .git is a
// pointer file rather than a directory.
func newWorktree(t *testing.T, mainRoot, name string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	target := filepath.Join(mainRoot, ".git", "worktrees", name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir worktrees entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+target+"\n"), 0o644); err != nil {
		t.Fatalf("write .git pointer: %v", err)
	}
	return wt
}

// ── FindRoot ──────────────────────────────────────────────────────────────────

func TestFindRoot_fromRepoRoot(t *testing.T) {
	root := newRepo(t, "")
	got, ok := FindRoot(root)
	if !ok {
		t.Fatal("FindRoot reported no repository at a repository root")
	}
	if got != root {
		t.Errorf("FindRoot = %q, want %q", got, root)
	}
}

func TestFindRoot_fromNestedDirectory(t *testing.T) {
	root := newRepo(t, "")
	nested := filepath.Join(root, "internal", "deep", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, ok := FindRoot(nested)
	if !ok {
		t.Fatal("FindRoot reported no repository from a nested directory")
	}
	if got != root {
		t.Errorf("FindRoot = %q, want the root %q", got, root)
	}
}

func TestFindRoot_outsideAnyRepository(t *testing.T) {
	// t.TempDir is not inside a repository, and the walk must terminate at the
	// filesystem root rather than looping.
	if _, ok := FindRoot(t.TempDir()); ok {
		t.Error("FindRoot found a repository where there is none")
	}
}

func TestFindRoot_worktreePointerCounts(t *testing.T) {
	main := newRepo(t, "")
	wt := newWorktree(t, main, "263_project-scope")
	got, ok := FindRoot(wt)
	if !ok {
		t.Fatal("FindRoot did not recognise a worktree as a repository")
	}
	if got != wt {
		t.Errorf("FindRoot = %q, want the worktree itself %q", got, wt)
	}
}

// ── Identity ──────────────────────────────────────────────────────────────────

func TestIdentity_plainCheckout(t *testing.T) {
	root := newRepo(t, "git@github.com:jophira/weft.git")
	repo, remote := Identity(root)
	if repo != filepath.Base(root) {
		t.Errorf("repo = %q, want %q", repo, filepath.Base(root))
	}
	if remote != "git@github.com:jophira/weft.git" {
		t.Errorf("remote = %q, want the origin url", remote)
	}
}

// The regression this package exists to avoid. rules.BuildContext returns no
// remote for a worktree, because .git is a file there. Losing the remote would
// disable every detect: remote.contains(...) rule in exactly the checkouts a
// worktree-based workflow does its work in.
func TestIdentity_worktreeResolvesRemoteViaPointer(t *testing.T) {
	main := newRepo(t, "git@github.com:jophira/weft.git")
	wt := newWorktree(t, main, "263_project-scope")

	repo, remote := Identity(wt)
	if repo != "263_project-scope" {
		t.Errorf("repo = %q, want the worktree directory name", repo)
	}
	if remote != "git@github.com:jophira/weft.git" {
		t.Errorf("remote = %q, want the main checkout's origin resolved through the pointer", remote)
	}
}

func TestIdentity_noRemoteIsEmptyNotAnError(t *testing.T) {
	root := newRepo(t, "")
	if _, remote := Identity(root); remote != "" {
		t.Errorf("remote = %q, want empty for a repository with no origin", remote)
	}
}

func TestIdentity_outsideRepositoryStillNamesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	repo, remote := Identity(dir)
	if repo != filepath.Base(dir) {
		t.Errorf("repo = %q, want the directory name even without git", repo)
	}
	if remote != "" {
		t.Errorf("remote = %q, want empty", remote)
	}
}

// ── EnsureExcluded ────────────────────────────────────────────────────────────

func readExclude(t *testing.T, gitDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(gitDir, "info", "exclude"))
	if err != nil {
		t.Fatalf("read exclude: %v", err)
	}
	return string(data)
}

func TestEnsureExcluded_writesEntryAndIsIdempotent(t *testing.T) {
	root := newRepo(t, "")
	wrote, err := EnsureExcluded(root)
	if err != nil {
		t.Fatalf("EnsureExcluded: %v", err)
	}
	if !wrote {
		t.Error("first EnsureExcluded reported no write")
	}
	got := readExclude(t, filepath.Join(root, ".git"))
	if !hasExcludeEntry(got, ExcludeEntry) {
		t.Errorf("exclude = %q, want it to contain %q", got, ExcludeEntry)
	}

	// Running again must not append a second copy.
	wrote2, err := EnsureExcluded(root)
	if err != nil {
		t.Fatalf("second EnsureExcluded: %v", err)
	}
	if wrote2 {
		t.Error("second EnsureExcluded wrote again, want idempotent")
	}
	if got2 := readExclude(t, filepath.Join(root, ".git")); got2 != got {
		t.Errorf("exclude changed on the second call:\n%q\nvs\n%q", got2, got)
	}
}

func TestEnsureExcluded_preservesExistingContent(t *testing.T) {
	root := newRepo(t, "")
	infoDir := filepath.Join(root, ".git", "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		t.Fatalf("mkdir info: %v", err)
	}
	existing := "# my own excludes\n*.local\n"
	if err := os.WriteFile(filepath.Join(infoDir, "exclude"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed exclude: %v", err)
	}

	if _, err := EnsureExcluded(root); err != nil {
		t.Fatalf("EnsureExcluded: %v", err)
	}
	got := readExclude(t, filepath.Join(root, ".git"))
	if !strings.Contains(got, "*.local") {
		t.Errorf("exclude = %q, lost the user's own entries", got)
	}
	if !hasExcludeEntry(got, ExcludeEntry) {
		t.Errorf("exclude = %q, missing weft's entry", got)
	}
}

func TestEnsureExcluded_respectsAUserWrittenEquivalent(t *testing.T) {
	root := newRepo(t, "")
	infoDir := filepath.Join(root, ".git", "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		t.Fatalf("mkdir info: %v", err)
	}
	// A different but equivalent spelling: adding a near-identical line would
	// just be noise.
	if err := os.WriteFile(filepath.Join(infoDir, "exclude"), []byte(".weft/\n"), 0o644); err != nil {
		t.Fatalf("seed exclude: %v", err)
	}
	wrote, err := EnsureExcluded(root)
	if err != nil {
		t.Fatalf("EnsureExcluded: %v", err)
	}
	if wrote {
		t.Error("wrote a duplicate entry over a user-written equivalent")
	}
}

// Hook delivery writes a settings file at a conventionally-ignored path, but a
// convention is not a rule: a fresh repository ignores nothing, so without the
// extra exclusion the "no tracked diff" delivery still leaves an untracked file
// in git status.
func TestEnsureExcluded_alsoExcludesHookWrittenFiles(t *testing.T) {
	root := newRepo(t, "")
	if _, err := EnsureExcluded(root, ".claude/settings.local.json"); err != nil {
		t.Fatalf("EnsureExcluded: %v", err)
	}
	got := readExclude(t, filepath.Join(root, ".git"))
	if !hasExcludeEntry(got, "/.claude/settings.local.json") {
		t.Errorf("exclude = %q, want the hook settings file excluded", got)
	}
	if !hasExcludeEntry(got, ExcludeEntry) {
		t.Errorf("exclude = %q, want the state dir excluded too", got)
	}
}

func TestEnsureExcluded_addsOnlyTheMissingPatterns(t *testing.T) {
	root := newRepo(t, "")
	if _, err := EnsureExcluded(root); err != nil {
		t.Fatalf("first: %v", err)
	}
	// The state dir is already there; only the settings path is new.
	wrote, err := EnsureExcluded(root, ".claude/settings.local.json")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !wrote {
		t.Fatal("reported no write when a new pattern was needed")
	}
	got := readExclude(t, filepath.Join(root, ".git"))
	if n := strings.Count(got, ExcludeEntry); n != 1 {
		t.Errorf("state dir entry appears %d times, want 1", n)
	}
}

func TestEnsureExcluded_worktreeUsesTheMainCheckout(t *testing.T) {
	main := newRepo(t, "")
	wt := newWorktree(t, main, "263_project-scope")

	if _, err := EnsureExcluded(wt); err != nil {
		t.Fatalf("EnsureExcluded on a worktree: %v", err)
	}
	// One entry in the shared info/exclude covers every worktree.
	got := readExclude(t, filepath.Join(main, ".git"))
	if !hasExcludeEntry(got, ExcludeEntry) {
		t.Errorf("main checkout exclude = %q, want weft's entry", got)
	}
}

func TestEnsureExcluded_outsideRepositoryIsNotAnError(t *testing.T) {
	wrote, err := EnsureExcluded(t.TempDir())
	if err != nil {
		t.Errorf("EnsureExcluded outside a repository: %v", err)
	}
	if wrote {
		t.Error("wrote an exclude where there is no git repository")
	}
}
