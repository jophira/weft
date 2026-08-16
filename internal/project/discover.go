package project

import (
	"os"
	"path/filepath"
	"strings"
)

// FindRoot walks up from dir looking for a repository root, returning it and
// whether one was found.
//
// "Repository root" means a directory holding a .git entry, which is a
// directory in an ordinary clone and a file in a worktree or submodule. Both
// count: a worktree is a working copy the user edits, and project rules apply
// there exactly as they do in the main checkout.
//
// The walk stops at the filesystem root. cf. Python: os.path.dirname in a loop
// until it stops changing, which is how Go spells "walk to the top" too, since
// filepath.Dir("/") == "/".
func FindRoot(dir string) (string, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
			return abs, true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", false
		}
		abs = parent
	}
}

// Identity returns the repository name and origin remote for root.
//
// The name is the directory's base name. The remote is read from the git config
// without shelling out, so it works when git is not installed.
//
// Worktrees are handled explicitly. In a worktree, .git is a file holding
// "gitdir: /path/to/main/.git/worktrees/<name>", so there is no config beside
// it and a naive read finds no remote at all. Since this user's workflow is
// worktree-heavy, silently losing the remote would disable every
// remote-matching rule (detect: remote.contains(...)) in exactly the checkouts
// the work happens in.
func Identity(root string) (repo, remote string) {
	repo = filepath.Base(root)
	if gitDir, ok := gitDirFor(root); ok {
		remote = originFromConfig(filepath.Join(gitDir, "config"))
	}
	return repo, remote
}

// gitDirFor resolves the directory holding the repository's config, following a
// worktree pointer to the main checkout when needed.
func gitDirFor(root string) (string, bool) {
	path := filepath.Join(root, ".git")
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return path, true
	}

	// A pointer file. Its target is <main>/.git/worktrees/<name>; the config
	// lives two levels up, in <main>/.git.
	data, err := os.ReadFile(path) //nolint:gosec // path is <root>/.git
	if err != nil {
		return "", false
	}
	target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if target == "" {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	// .../\.git/worktrees/<name> → .../\.git
	if parent := filepath.Dir(filepath.Dir(target)); filepath.Base(parent) == ".git" {
		return parent, true
	}
	return target, true
}

// originFromConfig extracts the [remote "origin"] url from a git config file,
// or "" when absent or unreadable.
func originFromConfig(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // path is a resolved .git/config
	if err != nil {
		return ""
	}
	inOrigin := false
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inOrigin = t == `[remote "origin"]`
			continue
		}
		if inOrigin {
			if key, val, ok := strings.Cut(t, "="); ok && strings.TrimSpace(key) == "url" {
				return strings.TrimSpace(val)
			}
		}
	}
	return ""
}
