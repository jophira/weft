package git

import (
	"fmt"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// Repo wraps a go-git repository for source sync operations.
type Repo struct {
	path string
}

func Open(path string) (*Repo, error) {
	if _, err := gogit.PlainOpen(path); err != nil {
		return nil, fmt.Errorf("opening repo at %s: %w", path, err)
	}
	return &Repo{path: path}, nil
}

func Init(path string) (*Repo, error) {
	if _, err := gogit.PlainInit(path, false); err != nil {
		return nil, fmt.Errorf("initialising repo at %s: %w", path, err)
	}
	return &Repo{path: path}, nil
}

func (r *Repo) Pull(auth transport.AuthMethod) error {
	repo, err := gogit.PlainOpen(r.path)
	if err != nil {
		return fmt.Errorf("opening repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}
	err = wt.Pull(&gogit.PullOptions{Auth: auth})
	if err == gogit.NoErrAlreadyUpToDate {
		return nil
	}
	return err
}

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

func (r *Repo) IsClean() (bool, error) {
	status, err := r.Status()
	if err != nil {
		return false, err
	}
	return status == "", nil
}
