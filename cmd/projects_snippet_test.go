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

func TestExpandProjectsPlaceholder_NoSourcesWithProjects(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "Before\n"+projectsPlaceholder+"\nAfter\n")

	srcs := []source.Source{
		{Name: "a", Root: t.TempDir()}, // no Structure.Projects
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

func TestExpandProjectsPlaceholder_OneSource(t *testing.T) {
	srcRoot := t.TempDir()
	projDir := filepath.Join(srcRoot, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projDir, "common.md"), "# Common")
	writeFile(t, filepath.Join(projDir, "myapp.md"), "# MyApp")

	stagedDir := t.TempDir()
	writeFile(t, filepath.Join(stagedDir, "CLAUDE.md"), "Preamble\n"+projectsPlaceholder+"\nTrailer\n")

	srcs := []source.Source{
		{Name: "src", Root: srcRoot, Structure: source.Structure{Projects: "projects/"}},
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
		t.Error("expected common.md entry in snippet")
	}
	if !strings.Contains(got, "{project-name}.md") {
		t.Error("expected {project-name}.md pattern in snippet")
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
		{Name: "a", Root: rootA, Structure: source.Structure{Projects: "projects/"}},
		{Name: "b", Root: rootB, Structure: source.Structure{Projects: "projects/"}},
	}
	if err := expandProjectsPlaceholder(stagedDir, srcs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, filepath.Join(stagedDir, "CLAUDE.md"))

	if !strings.Contains(got, filepath.Join(projA, "common.md")) {
		t.Errorf("missing rootA common.md in snippet; got:\n%s", got)
	}
	if !strings.Contains(got, filepath.Join(projB, "common.md")) {
		t.Errorf("missing rootB common.md in snippet; got:\n%s", got)
	}
	if !strings.Contains(got, filepath.Join(projB, "common-backend.md")) {
		t.Errorf("missing rootB common-backend.md in snippet; got:\n%s", got)
	}
	count := strings.Count(got, "{project-name}.md")
	if count != 2 {
		t.Errorf("expected 2 {project-name}.md entries, got %d", count)
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
		{Name: "src", Root: srcRoot, Structure: source.Structure{Projects: "projects/"}},
	}
	if err := expandProjectsPlaceholder(stagedDir, srcs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, filepath.Join(stagedDir, "CLAUDE.md"))
	if !strings.Contains(got, "common.md") {
		t.Errorf("expected fresh snippet with common.md; got:\n%s", got)
	}
	if !strings.Contains(got, "{project-name}.md") {
		t.Errorf("expected {project-name}.md in refreshed snippet; got:\n%s", got)
	}
	if !strings.Contains(got, "Intro") || !strings.Contains(got, "Outro") {
		t.Error("surrounding content was lost")
	}
}

// TestExpandProjectsPlaceholder_BothFormsPresent covers the unusual case where a
// file contains both the raw placeholder and an existing begin/end block (e.g. the
// placeholder appears above an older stale block). Both should be replaced.
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
		{Name: "src", Root: srcRoot, Structure: source.Structure{Projects: "projects/"}},
	}
	if err := expandProjectsPlaceholder(stagedDir, srcs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, filepath.Join(stagedDir, "CLAUDE.md"))
	if strings.Contains(got, projectsPlaceholder) {
		t.Error("raw placeholder was not replaced")
	}
	// The stale begin marker should be gone (replaced by the snippet from the raw placeholder).
	// After replacing the raw placeholder, the content contains the new snippet + old stale block;
	// then replaceProjectsBlock replaces the stale block with another copy of the snippet.
	// Either way, "common.md" must appear and no raw placeholder must remain.
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
	// Called with content that has no begin marker — must return content unchanged.
	// (Defensive guard; the caller checks hasBlock before calling, so unreachable
	// in practice, but the function must be safe to call either way.)
	content := "no markers here"
	got := replaceProjectsBlock(content, "replacement")
	if got != content {
		t.Errorf("expected content unchanged when begin marker is absent; got:\n%s", got)
	}
}

func TestGenerateProjectsSnippet_FiltersNonMdAndDirs(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// common.md → should appear
	writeFile(t, filepath.Join(projDir, "common.md"), "# Common")
	// common.txt → not .md, must be filtered
	writeFile(t, filepath.Join(projDir, "common.txt"), "ignored")
	// subdir → must be filtered
	if err := os.Mkdir(filepath.Join(projDir, "common-subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	// non-common .md → not a common* file, must not appear in always list
	writeFile(t, filepath.Join(projDir, "weft.md"), "# Weft")

	srcs := []source.Source{
		{Name: "s", Root: root, Structure: source.Structure{Projects: "projects/"}},
	}
	snippet := generateProjectsSnippet(srcs)

	if !strings.Contains(snippet, "common.md") {
		t.Errorf("expected common.md in snippet; got:\n%s", snippet)
	}
	if strings.Contains(snippet, "common.txt") {
		t.Errorf("common.txt should be filtered out; got:\n%s", snippet)
	}
	if strings.Contains(snippet, "common-subdir") {
		t.Errorf("subdirectory should be filtered out; got:\n%s", snippet)
	}
	// weft.md is not common* so it should not appear as an always-load entry,
	// only {project-name}.md pattern should represent project-specific files.
	if strings.Contains(snippet, "weft.md") {
		t.Errorf("non-common .md should not appear as always entry; got:\n%s", snippet)
	}
	if !strings.Contains(snippet, "{project-name}.md") {
		t.Errorf("expected {project-name}.md pattern; got:\n%s", snippet)
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
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(snippet, projectsBegin), projectsEnd))
	if inner != "" {
		t.Errorf("expected empty body, got: %q", inner)
	}
}

func TestGenerateProjectsSnippet_CommonFilesAlphabetical(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"common-z.md", "common-a.md", "common.md"} {
		writeFile(t, filepath.Join(projDir, name), "x")
	}

	srcs := []source.Source{
		{Name: "s", Root: root, Structure: source.Structure{Projects: "projects/"}},
	}
	snippet := generateProjectsSnippet(srcs)

	idxBase := strings.Index(snippet, "common.md")
	idxA := strings.Index(snippet, "common-a.md")
	idxZ := strings.Index(snippet, "common-z.md")
	if idxBase < 0 || idxA < 0 || idxZ < 0 {
		t.Fatalf("missing expected files in snippet:\n%s", snippet)
	}
	// os.ReadDir is alphabetical by byte value: '-' (45) < '.' (46),
	// so common-a.md < common-z.md < common.md.
	if idxA >= idxZ || idxZ >= idxBase {
		t.Errorf("files not in alphabetical order (common-a.md=%d common-z.md=%d common.md=%d)",
			idxA, idxZ, idxBase)
	}
}
