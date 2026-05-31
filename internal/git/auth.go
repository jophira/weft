package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// ResolveAuth returns the best available auth method for remote.
//   - SSH remotes (git@…, ssh://…): tries SSH agent, then common key files.
//   - HTTPS/local remotes: returns nil (go-git falls through to git credential helpers).
func ResolveAuth(remote string) (transport.AuthMethod, error) {
	if isSSH(remote) {
		return resolveSSH()
	}
	return nil, nil
}

func resolveSSH() (transport.AuthMethod, error) {
	// Prefer SSH agent — zero config for developers with ssh-add loaded.
	if auth, err := gossh.NewSSHAgentAuth("git"); err == nil {
		return auth, nil
	}

	// Fall back to the first available default key file.
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
	}
	for _, kf := range candidates {
		if _, err := os.Stat(kf); err != nil {
			continue
		}
		auth, err := gossh.NewPublicKeysFromFile("git", kf, "")
		if err == nil {
			return auth, nil
		}
	}

	return nil, fmt.Errorf(
		"no SSH auth available: SSH agent is not running and no key files found in ~/.ssh/\n" +
			"  start the agent:  eval $(ssh-agent) && ssh-add\n" +
			"  or generate a key: ssh-keygen -t ed25519",
	)
}

// isSSH returns true for git@ and ssh:// remote URLs.
func isSSH(url string) bool {
	return strings.HasPrefix(url, "git@") || strings.HasPrefix(url, "ssh://")
}
