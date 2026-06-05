package git

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh/agent"
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
	// Prefer SSH agent, but only when it actually has identities loaded.
	// NewSSHAgentAuth succeeds as long as the socket exists; it does not verify
	// that any keys are loaded, so we check explicitly to avoid a runtime failure
	// and fall through to key files when the agent is empty.
	if agentHasIdentities() {
		if auth, err := gossh.NewSSHAgentAuth("git"); err == nil {
			return auth, nil
		}
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
		"no SSH auth available: SSH agent has no identities and no usable key files found in ~/.ssh/\n" +
			"  load a key:       ssh-add ~/.ssh/id_ed25519\n" +
			"  or generate one:  ssh-keygen -t ed25519",
	)
}

// agentHasIdentities reports whether the SSH agent socket is reachable and
// has at least one identity loaded.
func agentHasIdentities() bool {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return false
	}
	conn, err := net.Dial("unix", sock) //nolint:gosec // SSH_AUTH_SOCK is a well-known user-session socket, not external input
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	keys, err := agent.NewClient(conn).List()
	return err == nil && len(keys) > 0
}

// isSSH returns true for git@ and ssh:// remote URLs.
func isSSH(url string) bool {
	return strings.HasPrefix(url, "git@") || strings.HasPrefix(url, "ssh://")
}
