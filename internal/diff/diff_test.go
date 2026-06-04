package diff_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/diff"
)

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

// ── Compare ───────────────────────────────────────────────────────────────────

func TestCompare_identical(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "CLAUDE.md", "rules")
	write(t, b, "CLAUDE.md", "rules")

	files, err := diff.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Kind != diff.Same {
		t.Errorf("expected 1 Same file, got %v", files)
	}
}

func TestCompare_added(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, b, "CLAUDE.md", "only in b")

	files, err := diff.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Kind != diff.Added {
		t.Errorf("expected 1 Added file, got %v", files)
	}
	if files[0].Rel != "CLAUDE.md" {
		t.Errorf("unexpected rel %q", files[0].Rel)
	}
}

func TestCompare_removed(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "CLAUDE.md", "only in a")

	files, err := diff.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Kind != diff.Removed {
		t.Errorf("expected 1 Removed file, got %v", files)
	}
}

func TestCompare_changed(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "CLAUDE.md", "version a")
	write(t, b, "CLAUDE.md", "version b")

	files, err := diff.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Kind != diff.Changed {
		t.Errorf("expected 1 Changed file, got %v", files)
	}
}

func TestCompare_mixed(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "CLAUDE.md", "same content")
	write(t, b, "CLAUDE.md", "same content")
	write(t, a, "commands/old.yaml", "cmd") // removed
	write(t, b, "commands/new.yaml", "cmd") // added
	write(t, a, "skills/foo.md", "v1")      // changed
	write(t, b, "skills/foo.md", "v2")      // changed

	files, err := diff.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	byRel := map[string]diff.Kind{}
	for _, f := range files {
		byRel[f.Rel] = f.Kind
	}
	if byRel["CLAUDE.md"] != diff.Same {
		t.Errorf("CLAUDE.md should be Same")
	}
	if byRel["commands/old.yaml"] != diff.Removed {
		t.Errorf("commands/old.yaml should be Removed")
	}
	if byRel["commands/new.yaml"] != diff.Added {
		t.Errorf("commands/new.yaml should be Added")
	}
	if byRel["skills/foo.md"] != diff.Changed {
		t.Errorf("skills/foo.md should be Changed")
	}
}

func TestCompare_sorted(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "zzz.md", "z")
	write(t, b, "aaa.md", "a")

	files, err := diff.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].Rel != "aaa.md" || files[1].Rel != "zzz.md" {
		t.Errorf("not sorted: %v", files)
	}
}

func TestCompare_bothEmpty(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	files, err := diff.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files for empty dirs, got %d", len(files))
	}
}

// ── LineDiff ──────────────────────────────────────────────────────────────────

func TestLineDiff_noChange(t *testing.T) {
	got := diff.LineDiff("line one\nline two\n", "line one\nline two\n")
	// All lines should be equal (no + or - prefixes).
	for _, line := range strings.Split(got, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "+") || strings.HasPrefix(strings.TrimSpace(line), "-") {
			// After stripping ANSI the prefix may be "  " not "+" or "-".
			// Check the raw prefix characters.
			if line[0] == '+' || line[0] == '-' {
				t.Errorf("unexpected change marker in equal diff: %q", line)
			}
		}
	}
}

func TestLineDiff_addedLine(t *testing.T) {
	got := diff.LineDiff("line one\n", "line one\nnew line\n")
	if !strings.Contains(got, "new line") {
		t.Errorf("expected added line in output, got:\n%s", got)
	}
}

func TestLineDiff_removedLine(t *testing.T) {
	got := diff.LineDiff("line one\nremoved\n", "line one\n")
	if !strings.Contains(got, "removed") {
		t.Errorf("expected removed line in output, got:\n%s", got)
	}
}

func TestLineDiff_emptyToNonEmpty(t *testing.T) {
	got := diff.LineDiff("", "hello\n")
	if !strings.Contains(got, "hello") {
		t.Errorf("expected 'hello' in output, got:\n%s", got)
	}
}

func TestLineDiff_nonEmptyToEmpty(t *testing.T) {
	got := diff.LineDiff("hello\n", "")
	if !strings.Contains(got, "hello") {
		t.Errorf("expected 'hello' in output, got:\n%s", got)
	}
}
