//go:build integration

package git_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogithttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jophira/weft/internal/git"
)

const (
	giteaUser  = "testadmin"
	giteaPass  = "Testadmin1!" // Gitea requires mixed-case + special char
	giteaEmail = "admin@weft.test"
)

// startGitea launches a Gitea container, creates an admin user, and returns the base URL.
func startGitea(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "gitea/gitea:1.22",
		ExposedPorts: []string{"3000/tcp"},
		Env: map[string]string{
			"GITEA__security__INSTALL_LOCK":                     "true",
			"GITEA__database__DB_TYPE":                          "sqlite3",
			"GITEA__server__HTTP_PORT":                          "3000",
			"GITEA__server__DISABLE_SSH":                        "true",
			"GITEA__log__LEVEL":                                 "Error",
			"GITEA__security__PASSWORD_COMPLEXITY":              "off",
			"GITEA__security__MIN_PASSWORD_LENGTH":              "6",
			"GITEA__service__DISABLE_REGISTRATION":              "false",
			"GITEA__service__REQUIRE_SIGNIN_VIEW":               "false",
			"GITEA__service__DEFAULT_ALLOW_CREATE_ORGANIZATION": "false",
		},
		WaitingFor: wait.ForHTTP("/api/v1/version").
			WithPort("3000/tcp").
			WithStartupTimeout(90 * time.Second),
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("starting gitea container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("getting container host: %v", err)
	}
	port, err := c.MappedPort(ctx, "3000")
	if err != nil {
		t.Fatalf("getting mapped port: %v", err)
	}
	baseURL := fmt.Sprintf("http://%s:%s", host, port.Port())

	// Create the admin user via the Gitea CLI, running as the git user (Gitea refuses root).
	code, execOut, execErr := c.Exec(ctx, []string{
		"/app/gitea/gitea", "admin", "user", "create",
		"--username", giteaUser,
		"--password", giteaPass,
		"--email", giteaEmail,
		"--admin",
		"--must-change-password=false",
	}, exec.WithUser("git"), exec.Multiplexed())
	if execErr != nil || code != 0 {
		var buf bytes.Buffer
		if execOut != nil {
			_, _ = io.Copy(&buf, execOut)
		}
		t.Fatalf("creating admin user: exit=%d err=%v output=%s", code, execErr, buf.String())
	}

	return baseURL
}

// createRepo creates a new repository via the Gitea REST API and returns its clone URL.
// The Gitea API returns an internal clone URL (localhost:3000); we rewrite it to use
// the externally-mapped baseURL so go-git can reach it from the host.
func createRepo(t *testing.T, baseURL, repoName string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"name":           repoName,
		"auto_init":      true,
		"default_branch": "main",
	})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/user/repos", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("building create-repo request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(giteaUser, giteaPass)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("creating repo: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating repo: status=%d body=%s", resp.StatusCode, body)
	}
	var out struct {
		CloneURL string `json:"clone_url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("parsing create-repo response: %v", err)
	}
	// Rewrite Gitea's self-referential URL (e.g. http://localhost:3000/...)
	// to the externally-mapped address so go-git can reach it from the host.
	cloneURL := strings.Replace(out.CloneURL, "http://localhost:3000", baseURL, 1)
	return cloneURL
}

// TestClone_httpBasicAuth verifies Clone works against a real Gitea server with basic auth.
func TestClone_httpBasicAuth(t *testing.T) {
	baseURL := startGitea(t)
	cloneURL := createRepo(t, baseURL, "rules")
	auth := &gogithttp.BasicAuth{Username: giteaUser, Password: giteaPass}

	dest := filepath.Join(t.TempDir(), "cloned")
	if err := git.Clone(cloneURL, dest, "main", auth, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	// The repo has at least the auto-init commit.
	r, err := git.Open(dest)
	if err != nil {
		t.Fatalf("Open after clone: %v", err)
	}
	branch, err := r.HeadBranch()
	if err != nil {
		t.Fatalf("HeadBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("HeadBranch = %q, want main", branch)
	}
}

// TestPush_httpBasicAuth clones a repo, commits a new file, and pushes back.
func TestPush_httpBasicAuth(t *testing.T) {
	baseURL := startGitea(t)
	cloneURL := createRepo(t, baseURL, "push-test")
	auth := &gogithttp.BasicAuth{Username: giteaUser, Password: giteaPass}

	local := filepath.Join(t.TempDir(), "local")
	if err := git.Clone(cloneURL, local, "main", auth, io.Discard); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	// Write a new file and commit.
	newFile := filepath.Join(local, "rules.md")
	if err := os.WriteFile(newFile, []byte("# Team rules\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	r, err := git.Open(local)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.CommitAll("add rules.md"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	if err := r.Push(auth); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Clone into a fresh directory and verify the file arrived.
	verify := filepath.Join(t.TempDir(), "verify")
	if err := git.Clone(cloneURL, verify, "main", auth, io.Discard); err != nil {
		t.Fatalf("Clone (verify): %v", err)
	}
	if _, err := os.Stat(filepath.Join(verify, "rules.md")); err != nil {
		t.Errorf("rules.md not found in fresh clone after push: %v", err)
	}
}

// TestResolveAuth_httpsReturnsNil confirms that HTTPS remotes get nil auth
// (go-git delegates to git credential helpers).
func TestResolveAuth_httpsReturnsNil(t *testing.T) {
	auth, err := git.ResolveAuth("https://github.com/example/repo.git")
	if err != nil {
		t.Fatalf("ResolveAuth: %v", err)
	}
	if auth != nil {
		t.Errorf("expected nil auth for HTTPS remote, got %T", auth)
	}
}

// TestResolveAuth_isSSH verifies that git@/ssh:// remotes trigger SSH resolution.
// In a CI environment without SSH keys this returns an error, which is expected.
func TestResolveAuth_isSSH(t *testing.T) {
	if os.Getenv("SSH_AUTH_SOCK") == "" && !hasSSHKey() {
		t.Skip("no SSH agent or key files available")
	}
	_, err := git.ResolveAuth("git@github.com:example/repo.git")
	// Either succeeds (key found) or errors cleanly (no key available).
	if err != nil && !strings.Contains(err.Error(), "no SSH auth") {
		t.Errorf("unexpected error from ResolveAuth for SSH remote: %v", err)
	}
}

// hasSSHKey reports whether any default key file exists.
func hasSSHKey() bool {
	home, _ := os.UserHomeDir()
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		if _, err := os.Stat(filepath.Join(home, ".ssh", name)); err == nil {
			return true
		}
	}
	return false
}
