package collect_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/collect"
)

// write creates a file at dir/rel with content, creating parent dirs.
func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ── Plain filename (no wildcards) ────────────────────────────────────────────

func TestCollect_plainFilename_found(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "# Rules")

	got, err := collect.Collect(root, "CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# Rules" {
		t.Errorf("got %q, want %q", string(got), "# Rules")
	}
}

func TestCollect_plainFilename_missing(t *testing.T) {
	root := t.TempDir()

	got, err := collect.Collect(root, "CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for missing file, got %q", string(got))
	}
}

func TestCollect_plainFilename_onlyReadRoot(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "root")
	write(t, root, "sub/CLAUDE.md", "sub") // should NOT be included

	got, err := collect.Collect(root, "CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "root" {
		t.Errorf("plain filename should only read root-level file, got %q", string(got))
	}
}

// ── Glob: walk and assemble ───────────────────────────────────────────────────

func TestCollect_glob_singleFile(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "only one")

	got, err := collect.Collect(root, "**/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "only one" {
		t.Errorf("got %q, want %q", string(got), "only one")
	}
}

func TestCollect_glob_noMatch(t *testing.T) {
	root := t.TempDir()
	write(t, root, "commands/foo.yaml", "not markdown")

	got, err := collect.Collect(root, "**/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil when no files match, got %q", string(got))
	}
}

func TestCollect_glob_emptyRoot(t *testing.T) {
	root := t.TempDir()

	got, err := collect.Collect(root, "**/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for empty root, got %q", string(got))
	}
}

// ── Walk order: parent-before-child, files-before-dirs ───────────────────────

func TestCollect_walkOrder_filesBeforeSubdirs(t *testing.T) {
	root := t.TempDir()
	// "Backend" sorts before "CLAUDE.md" alphabetically, but file must come first.
	write(t, root, "Backend/BACKEND.md", "[backend]")
	write(t, root, "CLAUDE.md", "[root]")

	got, err := collect.Collect(root, "**/*.md")
	if err != nil {
		t.Fatal(err)
	}
	// Root CLAUDE.md (file at root level) before Backend/BACKEND.md (in a subdir).
	want := "[root]\n[backend]"
	if string(got) != want {
		t.Errorf("order wrong:\ngot  %q\nwant %q", string(got), want)
	}
}

func TestCollect_walkOrder_deepHierarchy(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "global")
	write(t, root, "Backend/BACKEND.md", "backend")
	write(t, root, "Backend/Java/JAVA.md", "java")
	write(t, root, "Frontend/FRONTEND.md", "frontend")

	got, err := collect.Collect(root, "**/*.md")
	if err != nil {
		t.Fatal(err)
	}
	// Expected order: global → backend → java → frontend
	want := "global\nbackend\njava\nfrontend"
	if string(got) != want {
		t.Errorf("hierarchy order wrong:\ngot  %q\nwant %q", string(got), want)
	}
}

func TestCollect_walkOrder_alphabeticalWithinLevel(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Backend/BACKEND.md", "backend")
	write(t, root, "Analytics/ANALYTICS.md", "analytics") // A < B

	got, err := collect.Collect(root, "**/*.md")
	if err != nil {
		t.Fatal(err)
	}
	// Analytics sorts before Backend.
	want := "analytics\nbackend"
	if string(got) != want {
		t.Errorf("alphabetical order wrong:\ngot  %q\nwant %q", string(got), want)
	}
}

// ── Excludes ─────────────────────────────────────────────────────────────────

func TestCollect_excludes_skipsDirectory(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "rules")
	write(t, root, "skills/my-skill.md", "skill content") // should be excluded

	got, err := collect.Collect(root, "**/*.md", "skills")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "rules" {
		t.Errorf("got %q, wanted skills/ to be excluded", string(got))
	}
}

func TestCollect_excludes_trailingSlashNormalized(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "rules")
	write(t, root, "commands/cmd.md", "cmd") // should be excluded

	// Pass excludes with trailing slash — should still work.
	got, err := collect.Collect(root, "**/*.md", "commands/")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "rules" {
		t.Errorf("got %q, trailing slash in excludes not normalized", string(got))
	}
}

func TestCollect_excludes_multipleExcludes(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "rules")
	write(t, root, "skills/s.md", "skill")
	write(t, root, "commands/c.md", "cmd")
	write(t, root, "Backend/BACKEND.md", "backend")

	got, err := collect.Collect(root, "**/*.md", "skills", "commands")
	if err != nil {
		t.Fatal(err)
	}
	want := "rules\nbackend"
	if string(got) != want {
		t.Errorf("got %q, want %q", string(got), want)
	}
}

// ── Hidden files and dirs are skipped ────────────────────────────────────────

func TestCollect_skipsHiddenFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "visible")
	write(t, root, ".hidden.md", "hidden")

	got, err := collect.Collect(root, "**/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "visible" {
		t.Errorf("got %q, hidden file should be excluded", string(got))
	}
}

func TestCollect_skipsHiddenDirs(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "visible")
	write(t, root, ".hidden/HIDDEN.md", "in hidden dir")

	got, err := collect.Collect(root, "**/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "visible" {
		t.Errorf("got %q, file in hidden dir should be excluded", string(got))
	}
}

// ── Separator behavior ────────────────────────────────────────────────────────

func TestCollect_separatorNewlineAdded(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Backend/B.md", "backend") // no trailing newline
	write(t, root, "Frontend/F.md", "frontend")

	got, err := collect.Collect(root, "**/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "backend\nfrontend" {
		t.Errorf("separator wrong: %q", string(got))
	}
}

func TestCollect_separatorNotDoubled(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Backend/B.md", "backend\n") // already has trailing newline
	write(t, root, "Frontend/F.md", "frontend")

	got, err := collect.Collect(root, "**/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "backend\nfrontend" {
		t.Errorf("separator doubled: %q", string(got))
	}
}
