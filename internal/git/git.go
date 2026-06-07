package git

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cenkalti/backoff/v4"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// maxRetries is the number of attempts made for transient network operations.
// cf. Java: @Retryable(maxAttempts = 3) in Spring Retry
const maxRetries = 3

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
// This is a standalone probe used before Open() is called, so it always
// calls PlainOpen directly rather than relying on a cached handle.
func IsRepo(path string) bool {
	_, err := gogit.PlainOpen(path)
	return err == nil
}

// Repo is a handle to a local git repository.
// The gogit.Repository handle is cached at open time; it is safe to reuse
// across calls because go-git reads current on-disk state through the handle
// rather than snapshotting it at open time.
type Repo struct {
	path string
	repo *gogit.Repository // cached; never nil after Open returns successfully
}

// Open opens an existing repository at path.
func Open(path string) (*Repo, error) {
	r, err := gogit.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("opening repo at %s: %w", path, err)
	}
	return &Repo{path: path, repo: r}, nil
}

// Pull fetches from origin and fast-forwards to origin/<branch>.
// Returns (true, nil) when new commits were pulled, (false, nil) when already up to date.
// Transient network errors are retried up to maxRetries times with exponential back-off.
// cf. Java: Spring Retry / Resilience4j Retry — backoff.Retry is the Go equivalent.
func (r *Repo) Pull(branch string, auth transport.AuthMethod) (updated bool, err error) {
	wt, err := r.repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("getting worktree: %w", err)
	}

	// op is the operation passed to the retry loop.
	// errors are values — returning nil stops the loop, any non-nil error triggers a retry.
	op := func() error {
		pullErr := wt.Pull(&gogit.PullOptions{
			RemoteName:    "origin",
			ReferenceName: plumbing.NewBranchReferenceName(branch),
			Auth:          auth,
		})
		switch pullErr {
		case nil:
			updated = true
			return nil
		case gogit.NoErrAlreadyUpToDate:
			updated = false
			return nil // not an error; do not retry
		default:
			return pullErr // transient: let backoff decide whether to retry
		}
	}

	bo := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries)
	if retryErr := backoff.Retry(op, bo); retryErr != nil {
		return false, retryErr
	}
	return updated, nil
}

// Status returns the human-readable working-tree status string.
func (r *Repo) Status() (string, error) {
	wt, err := r.repo.Worktree()
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

// CommitAll stages every change in the working tree and creates a commit.
// Author name and email are read from the repo's local/global git config;
// if absent, "weft" / "weft@local" are used as fallbacks.
func (r *Repo) CommitAll(message string) error {
	wt, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}
	if err := wt.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		return fmt.Errorf("staging changes: %w", err)
	}
	name, email := authorFromConfig(r.repo)
	_, err = wt.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{Name: name, Email: email, When: time.Now()},
	})
	if err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

// authorFromConfig reads user.name and user.email from the repo's git config,
// falling back to "weft" / "weft@local" when not set.
func authorFromConfig(repo *gogit.Repository) (name, email string) {
	name, email = "weft", "weft@local"
	cfg, err := repo.ConfigScoped(0) // 0 = local + global merged
	if err != nil {
		return
	}
	if cfg.User.Name != "" {
		name = cfg.User.Name
	}
	if cfg.User.Email != "" {
		email = cfg.User.Email
	}
	return
}

// HeadBranch returns the name of the currently checked-out branch (e.g. "main").
func (r *Repo) HeadBranch() (string, error) {
	head, err := r.repo.Head()
	if err != nil {
		return "", fmt.Errorf("getting HEAD: %w", err)
	}
	return head.Name().Short(), nil
}

// OriginRemote returns the fetch URL of the "origin" remote, or "" if the
// repo has no origin configured.
func (r *Repo) OriginRemote() (string, error) {
	remote, err := r.repo.Remote("origin")
	if err == gogit.ErrRemoteNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading origin remote: %w", err)
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return "", nil
	}
	return urls[0], nil
}

// Push pushes all local commits on the current branch to origin.
// Returns nil when there is nothing new to push (already up to date).
// Transient network errors are retried up to maxRetries times with exponential back-off.
func (r *Repo) Push(auth transport.AuthMethod) error {
	op := func() error {
		pushErr := r.repo.Push(&gogit.PushOptions{
			RemoteName: "origin",
			Auth:       auth,
			Progress:   os.Stdout,
		})
		if pushErr == gogit.NoErrAlreadyUpToDate {
			return nil // not an error; do not retry
		}
		return pushErr // transient: let backoff decide whether to retry
	}

	bo := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries)
	return backoff.Retry(op, bo)
}
