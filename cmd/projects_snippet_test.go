package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/source"
)

func TestExpandProjectsPlaceholder_NoPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "# Rules\nNo placeholder here.\n")

	if err := expandProjectsPlaceholder(dir, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readFile(t, filepath.Join(dir, "CLAUDE.md"))
	if got != "# Rules\nNo placeholder here.\n" {
		t.Errorf("content changed unexpectedly: %q", got)
	}
}

func TestExpandProjectsPlaceholder_NoClaudeMd(t *testing.T) {
	dir := t.TempDir()
	if err := expandProjectsPlaceholder(dir, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExpandProjectsPlaceholder_NoProjectFilesFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "Before\n"+projectsPlaceholder+"\nAfter\n")

	srcs := []source.Source{
		{Name: "a", Root: t.TempDir()}, // empty source root — no project dirs
	}
	if err := expandProjectsPlaceholder(dir, srcs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readFile(t, filepath.Join(dir, "CLAUDE.md"))
	if strings.Contains(got, projectsPlaceholder) {
		t.Error("placeholder should have been replaced")
	}
	if !strings.Contains(got, projectsBegin) {
		t.Error("expected begin marker in output")
	}
}

func TestExpandProjectsPlaceholder_ExplicitProjectsPath(t *testing.T) {
	srcRoot := t.TempDir()
	projDir := filepath.Join(srcRoot, "my-rules")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projDir, "common.md"), "# Common")
	writeFile(t, filepath.Join(projDir, "myapp.md"), "# MyApp")

	stagedDir := t.TempDir()
	writeFile(t, filepath.Join(stagedDir, "CLAUDE.md"), "Preamble\n"+projectsPlaceholder+"\nTrailer\n")

	// "my-rules" is not a default project dir name, so explicit Projects field is needed.
	srcs := []source.Source{
		{Name: "src", Root: srcRoot, Structure: source.Structure{Projects: "my-rules/"}},
	}
	if err := expandProjectsPlaceholder(stagedDir, srcs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, filepath.Join(stagedDir, "CLAUDE.md"))

	if strings.Contains(got, projectsPlaceholder) {
		t.Error("placeholder was not replaced")
	}
	if !strings.Contains(got, projectsBegin) {
		t.Error("missing begin marker")
	}
	if !strings.Contains(got, projectsEnd) {
		t.Error("missing end marker")
	}
	if !strings.Contains(got, "common.md") {
		t.Errorf("expected common.md path in snippet; got:\n%s", got)
	}
	if !strings.Contains(got, "myapp.md") {
		t.Errorf("expected myapp.md path in snippet; got:\n%s", got)
	}
	if !strings.Contains(got, "Preamble") || !strings.Contains(got, "Trailer") {
		t.Error("surrounding content was lost")
	}
}

func TestExpandProjectsPlaceholder_TwoSources(t *testing.T) {
	rootA := t.TempDir()
	projA := filepath.Join(rootA, "projects")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projA, "common.md"), "# A common")

	rootB := t.TempDir()
	projB := filepath.Join(rootB, "projects")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projB, "common.md"), "# B common")
	writeFile(t, filepath.Join(projB, "common-backend.md"), "# B backend")

	stagedDir := t.TempDir()
	writeFile(t, filepath.Join(stagedDir, "CLAUDE.md"), projectsPlaceholder+"\n")

	srcs := []source.Source{
		{Name: "a", Root: rootA},
		{Name: "b", Root: rootB},
	}
	if err := expandProjectsPlaceholder(stagedDir, srcs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, filepath.Join(stagedDir, "CLAUDE.md"))

	// The snippet renders paths via locate.Tilde, which emits forward slashes
	// on every OS, so compare against the forward-slash form.
	if !strings.Contains(got, filepath.ToSlash(filepath.Join(projA, "common.md"))) {
		t.Errorf("missing rootA common.md in snippet; got:\n%s", got)
	}
	if !strings.Contains(got, filepath.ToSlash(filepath.Join(projB, "common.md"))) {
		t.Errorf("missing rootB common.md in snippet; got:\n%s", got)
	}
	if !strings.Contains(got, filepath.ToSlash(filepath.Join(projB, "common-backend.md"))) {
		t.Errorf("missing rootB common-backend.md in snippet; got:\n%s", got)
	}
}

// TestExpandProjectsPlaceholder_WriteBackPropagated covers the case where write-back
// has already propagated a previous (possibly stale/empty) begin/end block back into
// the source CLAUDE.md, replacing the raw placeholder. The expander must still refresh
// the block so the snippet is always current.
func TestExpandProjectsPlaceholder_WriteBackPropagated(t *testing.T) {
	srcRoot := t.TempDir()
	projDir := filepath.Join(srcRoot, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projDir, "common.md"), "# Common")

	stagedDir := t.TempDir()
	// Source already has the stale begin/end block (written back by write-back),
	// not the raw placeholder.
	staleBlock := projectsBegin + "\n" + projectsEnd
	writeFile(t, filepath.Join(stagedDir, "CLAUDE.md"), "Intro\n"+staleBlock+"\nOutro\n")

	srcs := []source.Source{
		{Name: "src", Root: srcRoot},
	}
	if err := expandProjectsPlaceholder(stagedDir, srcs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, filepath.Join(stagedDir, "CLAUDE.md"))
	if !strings.Contains(got, "common.md") {
		t.Errorf("expected fresh snippet with common.md; got:\n%s", got)
	}
	if !strings.Contains(got, "Intro") || !strings.Contains(got, "Outro") {
		t.Error("surrounding content was lost")
	}
}

// TestExpandProjectsPlaceholder_BothFormsPresent covers the unusual case where a
// file contains both the raw placeholder and an existing begin/end block.
func TestExpandProjectsPlaceholder_BothFormsPresent(t *testing.T) {
	srcRoot := t.TempDir()
	projDir := filepath.Join(srcRoot, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projDir, "common.md"), "# Common")

	stagedDir := t.TempDir()
	staleBlock := projectsBegin + "\n" + projectsEnd
	content := "A\n" + projectsPlaceholder + "\nB\n" + staleBlock + "\nC\n"
	writeFile(t, filepath.Join(stagedDir, "CLAUDE.md"), content)

	srcs := []source.Source{
		{Name: "src", Root: srcRoot},
	}
	if err := expandProjectsPlaceholder(stagedDir, srcs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, filepath.Join(stagedDir, "CLAUDE.md"))
	if strings.Contains(got, projectsPlaceholder) {
		t.Error("raw placeholder was not replaced")
	}
	if !strings.Contains(got, "common.md") {
		t.Errorf("expected common.md in output; got:\n%s", got)
	}
	if !strings.Contains(got, "A") || !strings.Contains(got, "C") {
		t.Error("surrounding content was lost")
	}
}

func TestReplaceProjectsBlock_MissingEndMarker(t *testing.T) {
	content := "before\n" + projectsBegin + "\nsome lines\n"
	got := replaceProjectsBlock(content, "replacement")
	if got != content {
		t.Errorf("expected content unchanged when end marker is missing; got:\n%s", got)
	}
}

func TestReplaceProjectsBlock_MissingBeginMarker(t *testing.T) {
	content := "no markers here"
	got := replaceProjectsBlock(content, "replacement")
	if got != content {
		t.Errorf("expected content unchanged when begin marker is absent; got:\n%s", got)
	}
}

func TestGenerateProjectsSnippet_FiltersNonMdAndHidden(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projDir, "common.md"), "# Common")
	writeFile(t, filepath.Join(projDir, "notes.txt"), "ignored")
	writeFile(t, filepath.Join(projDir, ".hidden.md"), "ignored")

	srcs := []source.Source{{Name: "s", Root: root}}
	snippet := generateProjectsSnippet(srcs)

	if !strings.Contains(snippet, "common.md") {
		t.Errorf("expected common.md in snippet; got:\n%s", snippet)
	}
	if strings.Contains(snippet, "notes.txt") {
		t.Errorf("non-.md file should be filtered; got:\n%s", snippet)
	}
	if strings.Contains(snippet, ".hidden.md") {
		t.Errorf("hidden file should be filtered; got:\n%s", snippet)
	}
}

func TestGenerateProjectsSnippet_AllMdFilesListed(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projDir, "common.md"), "# Common")
	writeFile(t, filepath.Join(projDir, "weft.md"), "# Weft")

	srcs := []source.Source{{Name: "s", Root: root}}
	snippet := generateProjectsSnippet(srcs)

	if !strings.Contains(snippet, "common.md") {
		t.Errorf("expected common.md in snippet; got:\n%s", snippet)
	}
	// all .md files are now listed explicitly — no more {project-name}.md pattern
	if !strings.Contains(snippet, "weft.md") {
		t.Errorf("expected weft.md in snippet; got:\n%s", snippet)
	}
	if strings.Contains(snippet, "{project-name}") {
		t.Errorf("snippet should not contain legacy {project-name} pattern; got:\n%s", snippet)
	}
}

func TestGenerateProjectsSnippet_EmptyWhenNoProjects(t *testing.T) {
	srcs := []source.Source{
		{Name: "a", Root: t.TempDir()},
	}
	snippet := generateProjectsSnippet(srcs)
	if !strings.Contains(snippet, projectsBegin) || !strings.Contains(snippet, projectsEnd) {
		t.Errorf("expected begin/end markers even for empty snippet: %q", snippet)
	}
	// Strip begin/end markers and verify there is no project file content.
	inner := strings.TrimPrefix(snippet, projectsBegin)
	inner = strings.TrimSuffix(inner, projectsEnd)
	if strings.Contains(inner, "`") {
		t.Errorf("expected no file paths in empty snippet, got: %q", inner)
	}
}

func TestGenerateProjectsSnippet_GroupedByProjectRoot(t *testing.T) {
	root := t.TempDir()
	phpSlug := filepath.Join(root, "php", "project-rules", "keyinvest")
	javaSlug := filepath.Join(root, "java", "project-rules", "svc")
	if err := os.MkdirAll(phpSlug, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(javaSlug, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(phpSlug, "keyinvest.md"), "# PHP")
	writeFile(t, filepath.Join(javaSlug, "svc.md"), "# Java")

	srcs := []source.Source{{Name: "s", Root: root}}
	snippet := generateProjectsSnippet(srcs)

	// Project roots are sorted alphabetically; java/ sorts before php/.
	javaRootIdx := strings.Index(snippet, "java/project-rules")
	phpRootIdx := strings.Index(snippet, "php/project-rules")
	if phpRootIdx < 0 || javaRootIdx < 0 {
		t.Fatalf("expected both group headers; got:\n%s", snippet)
	}
	if javaRootIdx >= phpRootIdx {
		t.Errorf("expected java group before php group (alphabetical); java=%d php=%d", javaRootIdx, phpRootIdx)
	}
	if !strings.Contains(snippet, "keyinvest.md") || !strings.Contains(snippet, "svc.md") {
		t.Errorf("expected both project files in snippet; got:\n%s", snippet)
	}
}

func TestGenerateProjectsSnippet_AlphabeticalOrder(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"common-z.md", "common-a.md", "common.md"} {
		writeFile(t, filepath.Join(projDir, name), "x")
	}

	srcs := []source.Source{{Name: "s", Root: root}}
	snippet := generateProjectsSnippet(srcs)

	idxA := strings.Index(snippet, "common-a.md")
	idxZ := strings.Index(snippet, "common-z.md")
	idxBase := strings.Index(snippet, "common.md")
	if idxA < 0 || idxZ < 0 || idxBase < 0 {
		t.Fatalf("missing expected files in snippet:\n%s", snippet)
	}
	// os.ReadDir is alphabetical by byte value: '-' (45) < '.' (46),
	// so common-a.md < common-z.md < common.md.
	if idxA >= idxZ || idxZ >= idxBase {
		t.Errorf("files not in alphabetical order (common-a.md=%d common-z.md=%d common.md=%d)",
			idxA, idxZ, idxBase)
	}
}

func TestGenerateProjectsSnippet_AutoDiscoversNestedProjectRoot(t *testing.T) {
	// Structure: work-tech-private/php/project-rules/ubs-keyinvest/ubs-keyinvest.md
	root := t.TempDir()
	slugDir := filepath.Join(root, "work-tech-private", "php", "project-rules", "ubs-keyinvest")
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(slugDir, "ubs-keyinvest.md"), "# UBS KeyInvest rules")

	srcs := []source.Source{{Name: "s", Root: root}}
	snippet := generateProjectsSnippet(srcs)

	if !strings.Contains(snippet, "ubs-keyinvest.md") {
		t.Errorf("expected ubs-keyinvest.md discovered via default name 'project-rules'; got:\n%s", snippet)
	}
}

func TestGenerateProjectsSnippet_MultipleProjectRoots(t *testing.T) {
	// Two language dirs, each with their own project-rules.
	root := t.TempDir()
	phpSlug := filepath.Join(root, "php", "project-rules", "keyinvest")
	javaSlug := filepath.Join(root, "java", "project-rules", "instrument-service")
	if err := os.MkdirAll(phpSlug, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(javaSlug, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(phpSlug, "keyinvest.md"), "# PHP rules")
	writeFile(t, filepath.Join(javaSlug, "instrument-service.md"), "# Java rules")

	srcs := []source.Source{{Name: "s", Root: root}}
	snippet := generateProjectsSnippet(srcs)

	if !strings.Contains(snippet, "keyinvest.md") {
		t.Errorf("expected keyinvest.md in snippet; got:\n%s", snippet)
	}
	if !strings.Contains(snippet, "instrument-service.md") {
		t.Errorf("expected instrument-service.md in snippet; got:\n%s", snippet)
	}
}

func TestGenerateProjectsSnippet_CustomProjectDirNames(t *testing.T) {
	root := t.TempDir()
	// Source uses "specs" as the project dir name — not a default.
	specsDir := filepath.Join(root, "nested", "specs")
	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(specsDir, "my-spec.md"), "# Spec")

	srcs := []source.Source{{
		Name: "s",
		Root: root,
		Structure: source.Structure{
			ProjectDirNames: []string{"specs"},
		},
	}}
	snippet := generateProjectsSnippet(srcs)

	if !strings.Contains(snippet, "my-spec.md") {
		t.Errorf("expected my-spec.md found via custom project dir name 'specs'; got:\n%s", snippet)
	}
}

func TestGenerateProjectsSnippet_RecursiveFilesUnderSlug(t *testing.T) {
	// Slug contains nested subdirs with .md files at multiple levels.
	root := t.TempDir()
	deepDir := filepath.Join(root, "projects", "my-project", "sub", "deep")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "projects", "my-project", "top.md"), "# Top")
	writeFile(t, filepath.Join(deepDir, "deep.md"), "# Deep")

	srcs := []source.Source{{Name: "s", Root: root}}
	snippet := generateProjectsSnippet(srcs)

	if !strings.Contains(snippet, "top.md") {
		t.Errorf("expected top.md in snippet; got:\n%s", snippet)
	}
	if !strings.Contains(snippet, "deep.md") {
		t.Errorf("expected deep.md in snippet; got:\n%s", snippet)
	}
}

func TestGenerateProjectsSnippet_ExplicitProjectsNotDuplicated(t *testing.T) {
	// When Projects field matches a default discovery name, ensure no duplicates.
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projDir, "rules.md"), "# Rules")

	srcs := []source.Source{{
		Name:      "s",
		Root:      root,
		Structure: source.Structure{Projects: "projects/"},
	}}
	snippet := generateProjectsSnippet(srcs)

	// File should appear exactly once — auto-discovery and explicit path both
	// resolve to the same dir and must be deduplicated.
	count := strings.Count(snippet, "rules.md")
	if count != 1 {
		t.Errorf("expected rules.md exactly once, got %d occurrences;\n%s", count, snippet)
	}
}

func TestFindProjectRoots_NonExistentRoot(t *testing.T) {
	roots, err := findProjectRoots("/nonexistent/path/xyz", map[string]bool{"projects": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roots) != 0 {
		t.Errorf("expected no roots for non-existent path, got: %v", roots)
	}
}

func TestCollectProjectFiles_SkipsHiddenDirs(t *testing.T) {
	root := t.TempDir()
	hiddenDir := filepath.Join(root, ".hidden")
	if err := os.MkdirAll(hiddenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(hiddenDir, "secret.md"), "# Secret")
	writeFile(t, filepath.Join(root, "visible.md"), "# Visible")

	files, err := collectProjectFiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range files {
		if strings.Contains(f, ".hidden") {
			t.Errorf("hidden dir should be skipped, got: %s", f)
		}
	}
	found := false
	for _, f := range files {
		if strings.HasSuffix(f, "visible.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected visible.md in results; got: %v", files)
	}
}
