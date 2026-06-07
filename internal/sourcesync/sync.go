// Package sourcesync provides the shared clone-or-pull logic used by both the
// MCP server (tools_source.go) and the CLI (cmd/source.go).
//
// The package name avoids "sync" to prevent shadowing the stdlib sync package.
package sourcesync

import (
	"fmt"
	"io"
	"os"

	"github.com/jophira/weft/internal/git"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/source"
)

// SyncSource clones (if missing) or pulls a single source.
// Returns true when new commits were fetched.
// out receives progress messages; pass io.Discard to suppress them.
//
// cf. Java: static utility method pattern — no receiver, pure function on the
// Source value type.
func SyncSource(s source.Source, out io.Writer) (bool, error) {
	if s.Remote == "" {
		return false, fmt.Errorf("source %q has no remote configured — add one with 'weft source edit --remote <url>'", s.Name)
	}

	// locate.ExpandHome expands ~/… to the absolute home-directory path.
	// cf. Python: os.path.expanduser("~/foo")
	expanded := locate.ExpandHome(s.Root)

	auth, err := git.ResolveAuth(s.Remote)
	if err != nil {
		return false, fmt.Errorf("resolving auth: %w", err)
	}

	// Clone path: directory does not yet exist.
	if _, err := os.Stat(expanded); os.IsNotExist(err) {
		fmt.Fprintf(out, "Cloning %s from %s...\n", s.Name, s.Remote)
		if err := git.Clone(s.Remote, expanded, s.Branch, auth, out); err != nil {
			return false, fmt.Errorf("clone failed: %w", err)
		}
		fmt.Fprintf(out, "✓ %s cloned → %s\n", s.Name, s.Root)
		return true, nil
	}

	// Path exists but is not a repo — stop before doing anything destructive.
	if !git.IsRepo(expanded) {
		return false, fmt.Errorf("%s exists but is not a git repository\n"+
			"  remove it or point the source to a different path", s.Root)
	}

	// Pull path: repo already exists locally.
	fmt.Fprintf(out, "Syncing %s (%s)...\n", s.Name, s.Root)
	repo, err := git.Open(expanded)
	if err != nil {
		return false, err
	}
	updated, err := repo.Pull(s.Branch, auth)
	if err != nil {
		return false, fmt.Errorf("pull failed: %w", err)
	}
	if updated {
		fmt.Fprintf(out, "✓ %s updated\n", s.Name)
	} else {
		fmt.Fprintf(out, "  %s already up to date\n", s.Name)
	}
	return updated, nil
}
