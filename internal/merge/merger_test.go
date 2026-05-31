package merge_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/merge"
	"github.com/jophira/weft/internal/profile"
)

// writeFile creates a file with given content inside dir, making parent dirs.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(data)
}

// ── Single source ─────────────────────────────────────────────────────────────

func TestMergeRoots_singleSource(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()
	writeFile(t, src, "CLAUDE.md", "# Rules")
	writeFile(t, src, "commands/hello.md", "say hi")

	manifest, err := merge.New(profile.OverlayCascade).MergeRoots([]string{src}, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	if len(manifest) != 2 {
		t.Errorf("manifest len = %d, want 2", len(manifest))
	}
	if got := readFile(t, out, "CLAUDE.md"); got != "# Rules" {
		t.Errorf("CLAUDE.md = %q, want %q", got, "# Rules")
	}
}

// ── Cascade: overlay wins on conflict ─────────────────────────────────────────

func TestMergeRoots_cascade_overlayWins(t *testing.T) {
	base := t.TempDir()
	overlay := t.TempDir()
	out := t.TempDir()

	writeFile(t, base, "CLAUDE.md", "base rules")
	writeFile(t, overlay, "CLAUDE.md", "overlay rules")

	_, err := merge.New(profile.OverlayCascade).MergeRoots([]string{base, overlay}, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	if got := readFile(t, out, "CLAUDE.md"); got != "overlay rules" {
		t.Errorf("CLAUDE.md = %q, want overlay to win", got)
	}
}

func TestMergeRoots_cascade_baseKeptWhenOverlayMissing(t *testing.T) {
	base := t.TempDir()
	overlay := t.TempDir()
	out := t.TempDir()

	writeFile(t, base, "CLAUDE.md", "base rules")
	writeFile(t, base, "commands/deploy.md", "deploy cmd")
	// overlay has no commands/deploy.md

	_, err := merge.New(profile.OverlayCascade).MergeRoots([]string{base, overlay}, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	if got := readFile(t, out, "commands/deploy.md"); got != "deploy cmd" {
		t.Errorf("commands/deploy.md = %q, want base value kept", got)
	}
}

// ── Append (merge strategy): both layers combined ─────────────────────────────

func TestMergeRoots_append_combinesContent(t *testing.T) {
	base := t.TempDir()
	overlay := t.TempDir()
	out := t.TempDir()

	writeFile(t, base, "CLAUDE.md", "# Base")
	writeFile(t, overlay, "CLAUDE.md", "# Overlay")

	_, err := merge.New(profile.OverlayMerge).MergeRoots([]string{base, overlay}, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	got := readFile(t, out, "CLAUDE.md")
	if got != "# Base\n# Overlay" {
		t.Errorf("CLAUDE.md = %q, want both sections combined", got)
	}
}

// ── LastWins ──────────────────────────────────────────────────────────────────

func TestMergeRoots_lastWins(t *testing.T) {
	base := t.TempDir()
	overlay := t.TempDir()
	out := t.TempDir()

	writeFile(t, base, "CLAUDE.md", "base")
	writeFile(t, overlay, "CLAUDE.md", "last")

	_, err := merge.New(profile.OverlayLastWins).MergeRoots([]string{base, overlay}, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	if got := readFile(t, out, "CLAUDE.md"); got != "last" {
		t.Errorf("CLAUDE.md = %q, want last", got)
	}
}

// ── Union of paths ────────────────────────────────────────────────────────────

func TestMergeRoots_unionOfPaths(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	out := t.TempDir()

	writeFile(t, a, "commands/alpha.md", "alpha")
	writeFile(t, b, "commands/beta.md", "beta")

	manifest, err := merge.New(profile.OverlayCascade).MergeRoots([]string{a, b}, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	if len(manifest) != 2 {
		t.Errorf("manifest len = %d, want 2 (union of both sources)", len(manifest))
	}
}

// ── Hidden files and dirs are skipped ─────────────────────────────────────────

func TestMergeRoots_skipsHidden(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()

	writeFile(t, src, "CLAUDE.md", "visible")
	writeFile(t, src, ".gitignore", "hidden file")
	writeFile(t, src, ".git/config", "git internals")

	manifest, err := merge.New(profile.OverlayCascade).MergeRoots([]string{src}, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	if len(manifest) != 1 {
		t.Errorf("manifest = %v, want only CLAUDE.md (hidden files skipped)", manifest)
	}
}

// ── WithFilter only processes matching paths ──────────────────────────────────

func TestMergeRoots_withFilter(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()

	writeFile(t, src, "CLAUDE.md", "rules")
	writeFile(t, src, "commands/hello.md", "cmd")
	writeFile(t, src, "sessions/abc.json", "internal state")
	writeFile(t, src, "cache/data.json", "cache")

	// Only include CLAUDE.md and commands/
	filter := func(rel string) bool {
		return rel == "CLAUDE.md" ||
			strings.HasPrefix(rel, "commands"+string(filepath.Separator))
	}

	manifest, err := merge.New(profile.OverlayCascade).WithFilter(filter).MergeRoots([]string{src}, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	if len(manifest) != 2 {
		t.Errorf("manifest = %v, want [CLAUDE.md commands/hello.md]", manifest)
	}
	// sessions and cache must not appear in output
	if _, err := os.Stat(filepath.Join(out, "sessions")); !os.IsNotExist(err) {
		t.Error("sessions/ should not be in output")
	}
}

// ── Root with a hidden name (e.g. ~/.claude) is NOT skipped ──────────────────

func TestMergeRoots_hiddenRootDirIsWalked(t *testing.T) {
	// Create a temp dir, then rename it to a hidden name to simulate ~/.claude.
	parent := t.TempDir()
	hiddenRoot := filepath.Join(parent, ".hidden-root")
	if err := os.Mkdir(hiddenRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, hiddenRoot, "CLAUDE.md", "rules from hidden root")

	out := t.TempDir()
	manifest, err := merge.New(profile.OverlayCascade).MergeRoots([]string{hiddenRoot}, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	if len(manifest) != 1 {
		t.Errorf("manifest = %v, want [CLAUDE.md] — hidden root dir was skipped", manifest)
	}
}

// ── Manifest is sorted ────────────────────────────────────────────────────────

func TestMergeRoots_manifestSorted(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()

	writeFile(t, src, "zzz.md", "z")
	writeFile(t, src, "aaa.md", "a")
	writeFile(t, src, "mmm.md", "m")

	manifest, err := merge.New(profile.OverlayCascade).MergeRoots([]string{src}, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	for i := 1; i < len(manifest); i++ {
		if manifest[i-1] > manifest[i] {
			t.Errorf("manifest not sorted: %v", manifest)
			break
		}
	}
}
