package git

import (
	"fmt"
	"io"
	"os"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// Clone clones url into path, checking out branch.
// auth may be nil for HTTPS repos that rely on system credential helpers.
// progress receives git's transfer output; pass os.Stdout for interactive use or io.Discard to silence it.
func Clone(url, path, branch string, auth transport.AuthMethod, progress io.Writer) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("creating target directory: %w", err)
	}
	_, err := gogit.PlainClone(path, false, &gogit.CloneOptions{
		URL:           url,
		Auth:          auth,
		Progress:      progress,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
	})
	if err != nil {
		return fmt.Errorf("clone from %s: %w", url, err)
	}
	return nil
}

// IsRepo reports whether path contains a git repository.
func IsRepo(path string) bool {
	_, err := gogit.PlainOpen(path)
	return err == nil
}

// Repo is a handle to a local git repository.
type Repo struct {
	path string
}

// Open opens an existing repository at path.
func Open(path string) (*Repo, error) {
	if _, err := gogit.PlainOpen(path); err != nil {
		return nil, fmt.Errorf("opening repo at %s: %w", path, err)
	}
	return &Repo{path: path}, nil
}

// Pull fetches from origin and fast-forwards to origin/<branch>.
// Returns (true, nil) when new commits were pulled, (false, nil) when already up to date.
func (r *Repo) Pull(branch string, auth transport.AuthMethod) (updated bool, err error) {
	repo, err := gogit.PlainOpen(r.path)
	if err != nil {
		return false, fmt.Errorf("opening repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("getting worktree: %w", err)
	}
	pullErr := wt.Pull(&gogit.PullOptions{
		RemoteName:    "origin",
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		Auth:          auth,
	})
	switch pullErr {
	case nil:
		return true, nil
	case gogit.NoErrAlreadyUpToDate:
		return false, nil
	default:
		return false, pullErr
	}
}

// Status returns the human-readable working-tree status string.
func (r *Repo) Status() (string, error) {
	repo, err := gogit.PlainOpen(r.path)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("getting worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return "", fmt.Errorf("getting status: %w", err)
	}
	return status.String(), nil
}

// IsClean reports whether the working tree has no uncommitted changes.
func (r *Repo) IsClean() (bool, error) {
	s, err := r.Status()
	if err != nil {
		return false, err
	}
	return s == "", nil
}

// HeadBranch returns the name of the currently checked-out branch (e.g. "main").
func (r *Repo) HeadBranch() (string, error) {
	repo, err := gogit.PlainOpen(r.path)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("getting HEAD: %w", err)
	}
	return head.Name().Short(), nil
}

// Push pushes all local commits on the current branch to origin.
// Returns nil when there is nothing new to push (already up to date).
func (r *Repo) Push(auth transport.AuthMethod) error {
	repo, err := gogit.PlainOpen(r.path)
	if err != nil {
		return fmt.Errorf("opening repo: %w", err)
	}
	err = repo.Push(&gogit.PushOptions{
		RemoteName: "origin",
		Auth:       auth,
		Progress:   os.Stdout,
	})
	if err == gogit.NoErrAlreadyUpToDate {
		return nil
	}
	return err
}
