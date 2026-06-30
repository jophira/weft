package rules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseOriginURL(t *testing.T) {
	cfg := `[core]
	repositoryformatversion = 0
[remote "upstream"]
	url = git@github.com:other/repo.git
[remote "origin"]
	url = git@github.com:jophira/weft.git
	fetch = +refs/heads/*:refs/remotes/origin/*
`
	if got := parseOriginURL(cfg); got != "git@github.com:jophira/weft.git" {
		t.Errorf("parseOriginURL = %q, want origin url", got)
	}
	if got := parseOriginURL("[core]\n\tbare = false\n"); got != "" {
		t.Errorf("expected empty when no origin, got %q", got)
	}
}

func TestBuildContext_RepoAndRemote(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "weft")
	gitDir := filepath.Join(repo, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"),
		[]byte("[remote \"origin\"]\n\turl = https://github.com/jophira/weft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, err := BuildContext(repo)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if ctx.Repo != "weft" {
		t.Errorf("Repo = %q, want weft", ctx.Repo)
	}
	if ctx.Remote != "https://github.com/jophira/weft" {
		t.Errorf("Remote = %q", ctx.Remote)
	}
}

func TestBuildContext_NoGitEmptyRemote(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, err := BuildContext(repo)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if ctx.Repo != "plain" || ctx.Remote != "" {
		t.Errorf("got Repo=%q Remote=%q, want plain / empty", ctx.Repo, ctx.Remote)
	}
}

// projectRulesTree writes a go rule and a project-scoped rule that detects the
// weft repo and extends go.
func projectRulesTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "go.md", "---\nlabel: go\ndetect: \"'go.mod' in files\"\npriority: 10\n---\nGO")
	writeFile(t, root, "projects/weft.md",
		"---\nlabel: project-weft\ndetect: \"repo == 'weft' || remote.contains('jophira/weft')\"\nextends: [go]\npriority: 100\n---\nWEFT PROJECT")
	return root
}

func TestResolve_ProjectScopedByRepoName(t *testing.T) {
	root := projectRulesTree(t)
	ev := newEvaluator(t)

	inWeft := Context{Files: []string{"go.mod"}, Repo: "weft"}
	res, err := Resolve(root, inWeft, ev)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	labels := loadedLabels(res)
	if !contains(labels, "project-weft") || !contains(labels, "go") {
		t.Errorf("expected go + project-weft in weft repo, got %v", labels)
	}

	elsewhere := Context{Files: []string{"go.mod"}, Repo: "other"}
	res2, err := Resolve(root, elsewhere, ev)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if contains(loadedLabels(res2), "project-weft") {
		t.Errorf("project-weft must not load outside its repo, got %v", loadedLabels(res2))
	}
}

func TestResolve_ProjectScopedByRemote(t *testing.T) {
	root := projectRulesTree(t)
	ctx := Context{Files: []string{"go.mod"}, Repo: "checkout-dir", Remote: "git@github.com:jophira/weft.git"}
	res, err := Resolve(root, ctx, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !contains(loadedLabels(res), "project-weft") {
		t.Errorf("expected project-weft via remote match, got %v", loadedLabels(res))
	}
}
