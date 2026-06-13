package merge_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/collect"
	"github.com/jophira/weft/internal/merge"
	"github.com/jophira/weft/internal/profile"
)

// writeFile creates a file with given content inside dir, making parent dirs.
// nr builds a []NamedRoot slice with empty Names from bare paths. Use this for
// tests that don't care about attribution markers — empty names suppress wrapping.
func nr(paths ...string) []merge.NamedRoot {
	result := make([]merge.NamedRoot, len(paths))
	for i, p := range paths {
		result[i] = merge.NamedRoot{Path: p}
	}
	return result
}

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

	manifest, _, err := merge.New(profile.OverlayCascade).MergeRoots(nr(src), out)
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

	_, _, err := merge.New(profile.OverlayCascade).MergeRoots(nr(base, overlay), out)
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

	_, _, err := merge.New(profile.OverlayCascade).MergeRoots(nr(base, overlay), out)
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

	_, _, err := merge.New(profile.OverlayMerge).MergeRoots(nr(base, overlay), out)
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

	_, _, err := merge.New(profile.OverlayLastWins).MergeRoots(nr(base, overlay), out)
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

	manifest, _, err := merge.New(profile.OverlayCascade).MergeRoots(nr(a, b), out)
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

	manifest, _, err := merge.New(profile.OverlayCascade).MergeRoots(nr(src), out)
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

	manifest, _, err := merge.New(profile.OverlayCascade).WithFilter(filter).MergeRoots(nr(src), out)
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
	manifest, _, err := merge.New(profile.OverlayCascade).MergeRoots(nr(hiddenRoot), out)
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

	manifest, _, err := merge.New(profile.OverlayCascade).MergeRoots(nr(src), out)
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

// ── WithAssembler: hierarchical instruction files ─────────────────────────────

// assemblerFor builds a merge.Assembler using collect.Collect with the given glob.
func assemblerFor(glob string, excludes ...string) merge.Assembler {
	return func(root string) ([]byte, error) {
		return collect.Collect(root, glob, excludes...)
	}
}

func TestMergeRoots_assembler_singleRoot_hierarchyAssembled(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()
	writeFile(t, src, "CLAUDE.md", "global")
	writeFile(t, src, "Backend/BACKEND.md", "backend")
	writeFile(t, src, "Frontend/FRONTEND.md", "frontend")

	_, _, err := merge.New(profile.OverlayCascade).
		WithAssembler(assemblerFor("**/*.md")).
		MergeRoots(nr(src), out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	got := readFile(t, out, "CLAUDE.md")
	want := "global\nbackend\nfrontend"
	if got != want {
		t.Errorf("assembled CLAUDE.md =\n%q\nwant\n%q", got, want)
	}
}

func TestMergeRoots_assembler_twoRoots_mergedWithCascade(t *testing.T) {
	base := t.TempDir()
	overlay := t.TempDir()
	out := t.TempDir()

	// base has a hierarchy; overlay has only a root CLAUDE.md override.
	writeFile(t, base, "CLAUDE.md", "base-global")
	writeFile(t, base, "Backend/BACKEND.md", "base-backend")
	writeFile(t, overlay, "CLAUDE.md", "overlay-rules")

	_, _, err := merge.New(profile.OverlayCascade).
		WithAssembler(assemblerFor("**/*.md")).
		MergeRoots(nr(base, overlay), out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	// Cascade: overlay wins — overlay's assembled content replaces base.
	got := readFile(t, out, "CLAUDE.md")
	if got != "overlay-rules" {
		t.Errorf("cascade should use overlay's assembled content, got %q", got)
	}
}

func TestMergeRoots_assembler_twoRoots_mergedWithAppend(t *testing.T) {
	base := t.TempDir()
	overlay := t.TempDir()
	out := t.TempDir()

	writeFile(t, base, "Backend/BACKEND.md", "base-backend")
	writeFile(t, overlay, "Frontend/FRONTEND.md", "overlay-frontend")

	_, _, err := merge.New(profile.OverlayMerge).
		WithAssembler(assemblerFor("**/*.md")).
		MergeRoots(nr(base, overlay), out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	// Merge strategy: both assembled contents concatenated.
	got := readFile(t, out, "CLAUDE.md")
	want := "base-backend\noverlay-frontend"
	if got != want {
		t.Errorf("append merge =\n%q\nwant\n%q", got, want)
	}
}

func TestMergeRoots_assembler_excludesHonoured(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()
	writeFile(t, src, "CLAUDE.md", "rules")
	writeFile(t, src, "skills/my-skill.md", "skill content") // should be excluded

	_, _, err := merge.New(profile.OverlayCascade).
		WithAssembler(assemblerFor("**/*.md", "skills")).
		MergeRoots(nr(src), out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	got := readFile(t, out, "CLAUDE.md")
	if got != "rules" {
		t.Errorf("skills/ content leaked into CLAUDE.md: %q", got)
	}
}

func TestMergeRoots_assembler_rootNoMatchContributesNothing(t *testing.T) {
	// base has no .md files → contributes nil → only overlay appears.
	base := t.TempDir()
	overlay := t.TempDir()
	out := t.TempDir()

	writeFile(t, base, "commands/foo.yaml", "cmd") // non-matching
	writeFile(t, overlay, "CLAUDE.md", "overlay-only")

	_, _, err := merge.New(profile.OverlayCascade).
		WithAssembler(assemblerFor("**/*.md")).
		MergeRoots(nr(base, overlay), out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	got := readFile(t, out, "CLAUDE.md")
	if got != "overlay-only" {
		t.Errorf("got %q, want only overlay content", got)
	}
}

// ── Attribution map ───────────────────────────────────────────────────────────

func TestMergeRoots_attribution_appendStrategy_multipleRoots(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	out := t.TempDir()
	writeFile(t, a, "CLAUDE.md", "from A")
	writeFile(t, b, "CLAUDE.md", "from B")

	_, attr, err := merge.New(profile.OverlayMerge).MergeRoots(nr(a, b), out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	got, ok := attr["CLAUDE.md"]
	if !ok {
		t.Fatal("attribution missing CLAUDE.md entry for multi-root AppendStrategy file")
	}
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("attribution[CLAUDE.md] = %v, want [0 1]", got)
	}
}

func TestMergeRoots_attribution_singleSource_noEntry(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()
	writeFile(t, src, "CLAUDE.md", "rules")

	_, attr, err := merge.New(profile.OverlayMerge).MergeRoots(nr(src), out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	if _, ok := attr["CLAUDE.md"]; ok {
		t.Errorf("single-source file should not appear in attribution, got %v", attr)
	}
}

func TestMergeRoots_attribution_onlyOneRootHasFile_noEntry(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	out := t.TempDir()
	writeFile(t, a, "CLAUDE.md", "only in A")
	// b has a different file entirely

	_, attr, err := merge.New(profile.OverlayCascade).MergeRoots(nr(a, b), out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	if _, ok := attr["CLAUDE.md"]; ok {
		t.Errorf("file present in only one root should not appear in attribution, got %v", attr)
	}
}

func TestMergeRoots_attribution_cascadeMultipleRoots_tracked(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	out := t.TempDir()
	writeFile(t, a, "CLAUDE.md", "base rules")
	writeFile(t, b, "CLAUDE.md", "overlay rules")

	_, attr, err := merge.New(profile.OverlayCascade).MergeRoots(nr(a, b), out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	got, ok := attr["CLAUDE.md"]
	if !ok {
		t.Fatal("attribution missing CLAUDE.md entry when both roots have the file")
	}
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("attribution[CLAUDE.md] = %v, want [0 1]", got)
	}
}

func TestMergeRoots_assembler_backwardCompat_plainFilename(t *testing.T) {
	// "CLAUDE.md" pattern = plain filename = backward compatible: reads only root file.
	src := t.TempDir()
	out := t.TempDir()
	writeFile(t, src, "CLAUDE.md", "root rules")
	writeFile(t, src, "Backend/BACKEND.md", "should not be assembled")

	_, _, err := merge.New(profile.OverlayCascade).
		WithAssembler(assemblerFor("CLAUDE.md")).
		MergeRoots(nr(src), out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	got := readFile(t, out, "CLAUDE.md")
	if got != "root rules" {
		t.Errorf("backward compat broken: got %q", got)
	}
}

// ── Source attribution markers ────────────────────────────────────────────────

func TestMergeRoots_attributionMarkers_addedForMultiSource(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	out := t.TempDir()
	writeFile(t, a, "CLAUDE.md", "# Source A rules")
	writeFile(t, b, "CLAUDE.md", "# Source B rules")

	roots := []merge.NamedRoot{
		{Name: "source-a", Path: a},
		{Name: "source-b", Path: b},
	}
	_, _, err := merge.New(profile.OverlayMerge).MergeRoots(roots, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	got := readFile(t, out, "CLAUDE.md")
	if !strings.Contains(got, `<!-- weft:source:begin name="source-a" -->`) {
		t.Errorf("expected source-a begin marker, got:\n%s", got)
	}
	if !strings.Contains(got, `<!-- weft:source:end name="source-a" -->`) {
		t.Errorf("expected source-a end marker, got:\n%s", got)
	}
	if !strings.Contains(got, `<!-- weft:source:begin name="source-b" -->`) {
		t.Errorf("expected source-b begin marker, got:\n%s", got)
	}
	if !strings.Contains(got, "# Source A rules") {
		t.Errorf("expected content from source-a, got:\n%s", got)
	}
	if !strings.Contains(got, "# Source B rules") {
		t.Errorf("expected content from source-b, got:\n%s", got)
	}
}

func TestMergeRoots_attributionMarkers_notAddedForSingleSource(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()
	writeFile(t, src, "CLAUDE.md", "# Rules")

	roots := []merge.NamedRoot{{Name: "my-source", Path: src}}
	_, _, err := merge.New(profile.OverlayMerge).MergeRoots(roots, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	got := readFile(t, out, "CLAUDE.md")
	if strings.Contains(got, "weft:source:begin") {
		t.Errorf("single-source should not have attribution markers, got:\n%s", got)
	}
	if got != "# Rules" {
		t.Errorf("single-source content should be unmodified, got %q", got)
	}
}

func TestMergeRoots_attributionMarkers_notAddedWhenNameEmpty(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	out := t.TempDir()
	writeFile(t, a, "CLAUDE.md", "# A")
	writeFile(t, b, "CLAUDE.md", "# B")

	// Roots with empty names → no markers, even with multiple contributors.
	_, _, err := merge.New(profile.OverlayMerge).MergeRoots(nr(a, b), out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	got := readFile(t, out, "CLAUDE.md")
	if strings.Contains(got, "weft:source:begin") {
		t.Errorf("empty-name roots should not produce attribution markers, got:\n%s", got)
	}
}

func TestMergeRoots_attributionMarkers_fileUniqueToOneRoot_noMarkers(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	out := t.TempDir()
	writeFile(t, a, "commands/deploy.md", "deploy cmd")
	// b does not have commands/deploy.md

	roots := []merge.NamedRoot{
		{Name: "source-a", Path: a},
		{Name: "source-b", Path: b},
	}
	_, _, err := merge.New(profile.OverlayCascade).MergeRoots(roots, out)
	if err != nil {
		t.Fatalf("MergeRoots: %v", err)
	}
	got := readFile(t, out, "commands/deploy.md")
	if strings.Contains(got, "weft:source") {
		t.Errorf("file unique to one root should not have markers, got:\n%s", got)
	}
	if got != "deploy cmd" {
		t.Errorf("content should be unmodified, got %q", got)
	}
}
