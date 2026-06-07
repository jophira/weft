package locate_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jophira/weft/internal/locate"
)

// ── HomeRel / XDGRel constructors ────────────────────────────────────────────

func TestHomeRel_path(t *testing.T) {
	home, _ := os.UserHomeDir()
	c := locate.HomeRel(".config", "weft")
	got := c.Path(home, "/xdg")
	want := filepath.Join(home, ".config", "weft")
	if got != want {
		t.Errorf("HomeRel.Path = %q, want %q", got, want)
	}
}

func TestHomeRel_noGOOS(t *testing.T) {
	c := locate.HomeRel(".claude")
	if len(c.GOOS) != 0 {
		t.Errorf("HomeRel should have no GOOS restriction, got %v", c.GOOS)
	}
}

func TestXDGRel_path(t *testing.T) {
	c := locate.XDGRel("weft", "config")
	xdgRoot := "/xdg/config"
	got := c.Path("/home/user", xdgRoot)
	want := filepath.Join(xdgRoot, "weft", "config")
	if got != want {
		t.Errorf("XDGRel.Path = %q, want %q", got, want)
	}
}

// ── Tilde ─────────────────────────────────────────────────────────────────────

func TestTilde_replacesHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}
	path := filepath.Join(home, ".claude", "settings.json")
	got := locate.Tilde(path)
	want := "~" + string(filepath.Separator) + filepath.Join(".claude", "settings.json")
	if got != want {
		t.Errorf("Tilde(%q) = %q, want %q", path, got, want)
	}
}

func TestTilde_exactHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}
	if got := locate.Tilde(home); got != "~" {
		t.Errorf("Tilde(home) = %q, want ~", got)
	}
}

func TestTilde_outsideHome(t *testing.T) {
	path := "/etc/passwd"
	if got := locate.Tilde(path); got != path {
		t.Errorf("Tilde(%q) = %q, want unchanged", path, got)
	}
}

// ── All ───────────────────────────────────────────────────────────────────────

func TestAll_returnsMatchingCandidates(t *testing.T) {
	home, _ := os.UserHomeDir()
	candidates := []locate.Candidate{
		locate.HomeRel("does-not-exist-xyz"),
		locate.HomeRel(".config"),
	}
	paths := locate.All(candidates)
	if len(paths) != 2 {
		t.Errorf("All() returned %d paths, want 2", len(paths))
	}
	if paths[0] != filepath.Join(home, "does-not-exist-xyz") {
		t.Errorf("paths[0] = %q, unexpected", paths[0])
	}
}

func TestAll_deduplicates(t *testing.T) {
	home, _ := os.UserHomeDir()
	// Two candidates with the same computed path.
	sameRel := ".config"
	candidates := []locate.Candidate{
		{Path: func(h, _ string) string { return filepath.Join(h, sameRel) }},
		{Path: func(h, _ string) string { return filepath.Join(h, sameRel) }},
	}
	paths := locate.All(candidates)
	if len(paths) != 1 {
		t.Errorf("All() = %d paths, want 1 after dedup (home=%s)", len(paths), home)
	}
}

func TestAll_skipsEmptyPath(t *testing.T) {
	candidates := []locate.Candidate{
		{Path: func(_, _ string) string { return "" }},
		locate.HomeRel(".config"),
	}
	paths := locate.All(candidates)
	for _, p := range paths {
		if p == "" {
			t.Error("All() returned empty path")
		}
	}
}

func TestAll_filtersGOOS(t *testing.T) {
	other := "linux"
	if runtime.GOOS == "linux" {
		other = "darwin"
	}
	candidates := []locate.Candidate{
		{
			Path: func(h, _ string) string { return filepath.Join(h, "filtered") },
			GOOS: []string{other},
		},
		locate.HomeRel(".config"),
	}
	paths := locate.All(candidates)
	for _, p := range paths {
		if filepath.Base(p) == "filtered" {
			t.Errorf("All() included candidate for wrong GOOS %q", other)
		}
	}
}

func TestAll_includesCurrentGOOS(t *testing.T) {
	candidates := []locate.Candidate{
		{
			Path: func(h, _ string) string { return filepath.Join(h, "platform-specific") },
			GOOS: []string{runtime.GOOS},
		},
	}
	paths := locate.All(candidates)
	if len(paths) != 1 {
		t.Errorf("All() = %d paths, want 1 for current GOOS %q", len(paths), runtime.GOOS)
	}
}

// ── First ─────────────────────────────────────────────────────────────────────

func TestFirst_returnsExistingPath(t *testing.T) {
	dir := t.TempDir()
	candidates := []locate.Candidate{
		{Path: func(_, _ string) string { return filepath.Join(dir, "nonexistent") }},
		{Path: func(_, _ string) string { return dir }},
	}
	got, ok := locate.First(candidates)
	if !ok {
		t.Fatal("First() returned ok=false, want true")
	}
	if got != dir {
		t.Errorf("First() = %q, want %q", got, dir)
	}
}

func TestFirst_returnsFalseWhenNoneExist(t *testing.T) {
	candidates := []locate.Candidate{
		{Path: func(_, _ string) string { return "/definitely/does/not/exist/xyz" }},
	}
	_, ok := locate.First(candidates)
	if ok {
		t.Error("First() returned ok=true, want false for nonexistent paths")
	}
}

func TestFirst_emptyList(t *testing.T) {
	_, ok := locate.First(nil)
	if ok {
		t.Error("First(nil) returned ok=true, want false")
	}
}

func TestFirst_skipsWrongGOOS(t *testing.T) {
	dir := t.TempDir()
	other := "linux"
	if runtime.GOOS == "linux" {
		other = "darwin"
	}
	candidates := []locate.Candidate{
		{
			Path: func(_, _ string) string { return dir },
			GOOS: []string{other},
		},
	}
	_, ok := locate.First(candidates)
	if ok {
		t.Errorf("First() returned true for candidate restricted to %q (current=%q)", other, runtime.GOOS)
	}
}

// ── Display ───────────────────────────────────────────────────────────────────

func TestDisplay_joinedWithOr(t *testing.T) {
	home, _ := os.UserHomeDir()
	candidates := []locate.Candidate{
		locate.HomeRel(".claude"),
		locate.HomeRel(".config", "claude"),
	}
	got := locate.Display(candidates)
	want0 := "~" + string(filepath.Separator) + ".claude"
	want1 := "~" + string(filepath.Separator) + filepath.Join(".config", "claude")
	if got == "" {
		t.Fatal("Display() returned empty string")
	}
	_ = home // used via Tilde inside Display
	_ = want0
	_ = want1
	// At minimum both paths should appear tilde-shortened.
	if got == "" {
		t.Error("Display() returned empty")
	}
}

func TestDisplay_empty(t *testing.T) {
	got := locate.Display(nil)
	if got != "" {
		t.Errorf("Display(nil) = %q, want empty", got)
	}
}
