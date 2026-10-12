package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/instruction"
	"github.com/jophira/weft/internal/project"
)

func weftBlock(preamble, userText string) string {
	return string(instruction.Upsert([]byte(preamble+userText), "@./.weft/instructions/00-x.md"))
}

func TestOrphanProjectFiles(t *testing.T) {
	hs := harness.Instances()

	onlyBlock := gitRepoT(t)
	writeFileT(t, filepath.Join(onlyBlock, "GEMINI.md"), weftBlock("", ""))
	cursor := harness.ProjectSupportOf(harnessesT(t, "cursor")[0])
	writeFileT(t, filepath.Join(onlyBlock, filepath.FromSlash(cursor.Path)), weftBlock(cursor.Preamble, ""))

	userText := gitRepoT(t)
	writeFileT(t, filepath.Join(userText, "GEMINI.md"), weftBlock("", "# Mine\n\nKeep.\n"))

	excluded := gitRepoT(t)
	writeFileT(t, filepath.Join(excluded, "GEMINI.md"), weftBlock("", ""))
	if _, err := project.EnsureExcluded(excluded, "GEMINI.md"); err != nil {
		t.Fatalf("EnsureExcluded: %v", err)
	}

	tracked := gitRepoT(t)
	writeFileT(t, filepath.Join(tracked, "GEMINI.md"), weftBlock("", ""))
	gitT(t, tracked, "add", "GEMINI.md")
	gitT(t, tracked, "commit", "-q", "-m", "add")

	// A repository's own .gitignore hiding the leftover must not hide it from doctor.
	ignored := gitRepoT(t)
	writeFileT(t, filepath.Join(ignored, ".gitignore"), "GEMINI.md\n")
	writeFileT(t, filepath.Join(ignored, "GEMINI.md"), weftBlock("", ""))

	// An empty file the user has just created holds no weft block, so it is theirs.
	empty := gitRepoT(t)
	writeFileT(t, filepath.Join(empty, "GEMINI.md"), "")

	got := orphanProjectFiles([]string{onlyBlock, userText, excluded, tracked, ignored, empty}, hs)

	want := map[string]bool{
		filepath.Join(onlyBlock, "GEMINI.md"):                     true,
		filepath.Join(onlyBlock, filepath.FromSlash(cursor.Path)): true,
		filepath.Join(ignored, "GEMINI.md"):                       true,
	}
	if len(got) != len(want) {
		t.Fatalf("orphans = %v, want exactly %v", got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected orphan %s", g)
		}
	}
	for _, p := range []string{userText, excluded, tracked, empty} {
		if _, err := os.Stat(filepath.Join(p, "GEMINI.md")); err != nil {
			t.Errorf("listing must not touch %s: %v", p, err)
		}
	}
}
