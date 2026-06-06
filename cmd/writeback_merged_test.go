package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/watch"
)

// ── rebuildMerged ─────────────────────────────────────────────────────────────

func TestRebuildMerged_TwoBodies(t *testing.T) {
	got := rebuildMerged([][]byte{[]byte("A\nB\n"), []byte("C\n")})
	if string(got) != "A\nB\nC\n" {
		t.Errorf("got %q, want %q", got, "A\nB\nC\n")
	}
}

func TestRebuildMerged_AddsNewlineSeparator(t *testing.T) {
	got := rebuildMerged([][]byte{[]byte("A\nB"), []byte("C\n")})
	if string(got) != "A\nB\nC\n" {
		t.Errorf("got %q, want %q", got, "A\nB\nC\n")
	}
}

func TestRebuildMerged_SkipsNilBodies(t *testing.T) {
	got := rebuildMerged([][]byte{[]byte("A\n"), nil, []byte("B\n")})
	if string(got) != "A\nB\n" {
		t.Errorf("got %q, want %q", got, "A\nB\n")
	}
}

// ── mergedLineBoundaries ──────────────────────────────────────────────────────

func TestMergedLineBoundaries_BothEndWithNL(t *testing.T) {
	bodies := [][]byte{[]byte("line0\nline1\n"), []byte("line2\nline3\n")}
	bounds := mergedLineBoundaries(bodies)
	if bounds[0] != [2]int{0, 1} {
		t.Errorf("bounds[0] = %v, want [0 1]", bounds[0])
	}
	if bounds[1] != [2]int{2, 3} {
		t.Errorf("bounds[1] = %v, want [2 3]", bounds[1])
	}
}

func TestMergedLineBoundaries_FirstLacksTrailingNL(t *testing.T) {
	// body0 has no trailing \n → AppendStrategy inserts separator
	bodies := [][]byte{[]byte("line0\nline1"), []byte("line2\nline3\n")}
	bounds := mergedLineBoundaries(bodies)
	if bounds[0] != [2]int{0, 1} {
		t.Errorf("bounds[0] = %v, want [0 1]", bounds[0])
	}
	if bounds[1] != [2]int{2, 3} {
		t.Errorf("bounds[1] = %v, want [2 3]", bounds[1])
	}
}

func TestMergedLineBoundaries_EmptyBody(t *testing.T) {
	bodies := [][]byte{[]byte("A\n"), nil, []byte("B\n")}
	bounds := mergedLineBoundaries(bodies)
	if bounds[0] != [2]int{0, 0} {
		t.Errorf("bounds[0] = %v, want [0 0]", bounds[0])
	}
	if bounds[1] != [2]int{-1, -1} {
		t.Errorf("bounds[1] = %v, want [-1 -1]", bounds[1])
	}
	if bounds[2] != [2]int{1, 1} {
		t.Errorf("bounds[2] = %v, want [1 1]", bounds[2])
	}
}

// ── splitLines ────────────────────────────────────────────────────────────────

func TestSplitLines(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"a\nb\n", []string{"a", "b"}},
		{"a\nb", []string{"a", "b"}},
		{"", nil},
		{"\n", []string{""}},
	}
	for _, tc := range cases {
		got := splitLines(tc.input)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("splitLines(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ── lcsEditScript ─────────────────────────────────────────────────────────────

func TestLCSEditScript_NoChange(t *testing.T) {
	lines := []string{"a", "b", "c"}
	ops := lcsEditScript(lines, lines)
	for _, op := range ops {
		if op.kind != 'e' {
			t.Errorf("expected all equal ops, got kind %c", op.kind)
		}
	}
}

func TestLCSEditScript_InPlaceEdit(t *testing.T) {
	baseline := []string{"a", "b", "c"}
	edited := []string{"a", "B", "c"}
	ops := lcsEditScript(baseline, edited)

	kinds := make([]byte, len(ops))
	for i, op := range ops {
		kinds[i] = op.kind
	}
	// Should be: equal(a), delete(b), insert(B), equal(c)
	expected := []byte{'e', 'd', 'i', 'e'}
	if string(kinds) != string(expected) {
		t.Errorf("op kinds = %s, want %s", kinds, expected)
	}
}

func TestLCSEditScript_Insert(t *testing.T) {
	baseline := []string{"a", "c"}
	edited := []string{"a", "b", "c"}
	ops := lcsEditScript(baseline, edited)
	kinds := make([]byte, len(ops))
	for i, op := range ops {
		kinds[i] = op.kind
	}
	// equal(a), insert(b), equal(c)
	expected := []byte{'e', 'i', 'e'}
	if string(kinds) != string(expected) {
		t.Errorf("op kinds = %s, want %s", kinds, expected)
	}
}

func TestLCSEditScript_Delete(t *testing.T) {
	baseline := []string{"a", "b", "c"}
	edited := []string{"a", "c"}
	ops := lcsEditScript(baseline, edited)
	kinds := make([]byte, len(ops))
	for i, op := range ops {
		kinds[i] = op.kind
	}
	// equal(a), delete(b), equal(c)
	expected := []byte{'e', 'd', 'e'}
	if string(kinds) != string(expected) {
		t.Errorf("op kinds = %s, want %s", kinds, expected)
	}
}

// ── writeBackMergedSource ─────────────────────────────────────────────────────

func TestWriteBackMergedSource_EditInFirstSource(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()
	srcBRoot := t.TempDir()

	// Source A: 2 lines; Source B: 2 lines.
	writeFile(t, filepath.Join(srcARoot, "CLAUDE.md"), "work rule 1\nwork rule 2\n")
	writeFile(t, filepath.Join(srcBRoot, "CLAUDE.md"), "personal rule 1\npersonal rule 2\n")
	// Harness edited work rule 2.
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), "work rule 1\nWORK RULE 2 EDITED\npersonal rule 1\npersonal rule 2\n")

	srcs := []source.Source{newSource("work", srcARoot), newSource("personal", srcBRoot)}
	p := newProfile(srcs)
	m := &manifest.Manifest{
		Files: map[string]string{"CLAUDE.md": "sha256:abc"},
		SourceFiles: map[string][]string{
			"CLAUDE.md": {"work", "personal"},
		},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "CLAUDE.md"}

	performed, err := writeBackMergedSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackMergedSource: %v", err)
	}
	if !performed {
		t.Fatal("expected performed=true")
	}

	// Source A should have the edited line.
	if got := readFile(t, filepath.Join(srcARoot, "CLAUDE.md")); got != "work rule 1\nWORK RULE 2 EDITED\n" {
		t.Errorf("source A CLAUDE.md = %q, want %q", got, "work rule 1\nWORK RULE 2 EDITED\n")
	}
	// Source B should be unchanged.
	if got := readFile(t, filepath.Join(srcBRoot, "CLAUDE.md")); got != "personal rule 1\npersonal rule 2\n" {
		t.Errorf("source B CLAUDE.md = %q, want %q", got, "personal rule 1\npersonal rule 2\n")
	}
}

func TestWriteBackMergedSource_EditInSecondSource(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()
	srcBRoot := t.TempDir()

	writeFile(t, filepath.Join(srcARoot, "CLAUDE.md"), "work rule 1\n")
	writeFile(t, filepath.Join(srcBRoot, "CLAUDE.md"), "personal rule 1\npersonal rule 2\n")
	// Harness edited personal rule 1.
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), "work rule 1\nPERSONAL RULE 1 EDITED\npersonal rule 2\n")

	srcs := []source.Source{newSource("work", srcARoot), newSource("personal", srcBRoot)}
	p := newProfile(srcs)
	m := &manifest.Manifest{
		Files:       map[string]string{"CLAUDE.md": "sha256:abc"},
		SourceFiles: map[string][]string{"CLAUDE.md": {"work", "personal"}},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "CLAUDE.md"}

	performed, err := writeBackMergedSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackMergedSource: %v", err)
	}
	if !performed {
		t.Fatal("expected performed=true")
	}

	if got := readFile(t, filepath.Join(srcARoot, "CLAUDE.md")); got != "work rule 1\n" {
		t.Errorf("source A unchanged = %q, want %q", got, "work rule 1\n")
	}
	if got := readFile(t, filepath.Join(srcBRoot, "CLAUDE.md")); got != "PERSONAL RULE 1 EDITED\npersonal rule 2\n" {
		t.Errorf("source B CLAUDE.md = %q, want %q", got, "PERSONAL RULE 1 EDITED\npersonal rule 2\n")
	}
}

func TestWriteBackMergedSource_AppendToFirstSource(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()
	srcBRoot := t.TempDir()

	writeFile(t, filepath.Join(srcARoot, "CLAUDE.md"), "work rule 1\n")
	writeFile(t, filepath.Join(srcBRoot, "CLAUDE.md"), "personal rule 1\n")
	// Harness inserted a new work rule at the boundary between A and B.
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), "work rule 1\nwork rule 2\npersonal rule 1\n")

	srcs := []source.Source{newSource("work", srcARoot), newSource("personal", srcBRoot)}
	p := newProfile(srcs)
	m := &manifest.Manifest{
		Files:       map[string]string{"CLAUDE.md": "sha256:abc"},
		SourceFiles: map[string][]string{"CLAUDE.md": {"work", "personal"}},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "CLAUDE.md"}

	performed, err := writeBackMergedSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackMergedSource: %v", err)
	}
	if !performed {
		t.Fatal("expected performed=true")
	}

	// Inserted line attributed to the preceding source (work).
	if got := readFile(t, filepath.Join(srcARoot, "CLAUDE.md")); got != "work rule 1\nwork rule 2\n" {
		t.Errorf("source A = %q, want %q", got, "work rule 1\nwork rule 2\n")
	}
	if got := readFile(t, filepath.Join(srcBRoot, "CLAUDE.md")); got != "personal rule 1\n" {
		t.Errorf("source B unchanged = %q, want %q", got, "personal rule 1\n")
	}
}

func TestWriteBackMergedSource_DeleteLineFromFirstSource(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()
	srcBRoot := t.TempDir()

	writeFile(t, filepath.Join(srcARoot, "CLAUDE.md"), "work rule 1\nwork rule 2\n")
	writeFile(t, filepath.Join(srcBRoot, "CLAUDE.md"), "personal rule 1\n")
	// Harness deleted work rule 2.
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), "work rule 1\npersonal rule 1\n")

	srcs := []source.Source{newSource("work", srcARoot), newSource("personal", srcBRoot)}
	p := newProfile(srcs)
	m := &manifest.Manifest{
		Files:       map[string]string{"CLAUDE.md": "sha256:abc"},
		SourceFiles: map[string][]string{"CLAUDE.md": {"work", "personal"}},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "CLAUDE.md"}

	performed, err := writeBackMergedSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackMergedSource: %v", err)
	}
	if !performed {
		t.Fatal("expected performed=true")
	}

	if got := readFile(t, filepath.Join(srcARoot, "CLAUDE.md")); got != "work rule 1\n" {
		t.Errorf("source A = %q, want %q", got, "work rule 1\n")
	}
	if got := readFile(t, filepath.Join(srcBRoot, "CLAUDE.md")); got != "personal rule 1\n" {
		t.Errorf("source B unchanged = %q, want %q", got, "personal rule 1\n")
	}
}

func TestWriteBackMergedSource_NoChange(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()
	srcBRoot := t.TempDir()

	writeFile(t, filepath.Join(srcARoot, "CLAUDE.md"), "work rule\n")
	writeFile(t, filepath.Join(srcBRoot, "CLAUDE.md"), "personal rule\n")
	// Target matches the expected baseline — no change.
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), "work rule\npersonal rule\n")

	srcs := []source.Source{newSource("work", srcARoot), newSource("personal", srcBRoot)}
	p := newProfile(srcs)
	m := &manifest.Manifest{
		Files:       map[string]string{"CLAUDE.md": "sha256:abc"},
		SourceFiles: map[string][]string{"CLAUDE.md": {"work", "personal"}},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "CLAUDE.md"}

	performed, err := writeBackMergedSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackMergedSource: %v", err)
	}
	if performed {
		t.Error("expected performed=false when target matches baseline")
	}
}

func TestWriteBackMergedSource_SingleSourceSkipped(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()

	writeFile(t, filepath.Join(srcARoot, "CLAUDE.md"), "rule\n")
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), "edited rule\n")

	srcs := []source.Source{newSource("work", srcARoot)}
	p := newProfile(srcs)
	m := &manifest.Manifest{
		Files: map[string]string{"CLAUDE.md": "sha256:abc"},
		// No SourceFiles entry → single-source → skip.
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "CLAUDE.md"}

	performed, err := writeBackMergedSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackMergedSource: %v", err)
	}
	if performed {
		t.Error("expected performed=false for single-source file")
	}
}

func TestWriteBackMergedSource_ThreeSources(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()
	srcBRoot := t.TempDir()
	srcCRoot := t.TempDir()

	writeFile(t, filepath.Join(srcARoot, "CLAUDE.md"), "A1\nA2\n")
	writeFile(t, filepath.Join(srcBRoot, "CLAUDE.md"), "B1\n")
	writeFile(t, filepath.Join(srcCRoot, "CLAUDE.md"), "C1\nC2\n")
	// Harness edited B1 → BEDIT and C1 → CEDIT.
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), "A1\nA2\nBEDIT\nCEDIT\nC2\n")

	srcs := []source.Source{
		newSource("work", srcARoot),
		newSource("team", srcBRoot),
		newSource("personal", srcCRoot),
	}
	p := &profile.Profile{
		Name:    "test",
		Sources: []string{"work", "team", "personal"},
		Overlay: profile.OverlayMerge,
	}
	m := &manifest.Manifest{
		Files:       map[string]string{"CLAUDE.md": "sha256:abc"},
		SourceFiles: map[string][]string{"CLAUDE.md": {"work", "team", "personal"}},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "CLAUDE.md"}

	performed, err := writeBackMergedSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackMergedSource: %v", err)
	}
	if !performed {
		t.Fatal("expected performed=true")
	}

	if got := readFile(t, filepath.Join(srcARoot, "CLAUDE.md")); got != "A1\nA2\n" {
		t.Errorf("source A = %q, want %q", got, "A1\nA2\n")
	}
	if got := readFile(t, filepath.Join(srcBRoot, "CLAUDE.md")); got != "BEDIT\n" {
		t.Errorf("source B = %q, want %q", got, "BEDIT\n")
	}
	if got := readFile(t, filepath.Join(srcCRoot, "CLAUDE.md")); got != "CEDIT\nC2\n" {
		t.Errorf("source C = %q, want %q", got, "CEDIT\nC2\n")
	}
}

func TestWriteBackMergedSource_AppendAtEnd(t *testing.T) {
	targetRoot := t.TempDir()
	srcARoot := t.TempDir()
	srcBRoot := t.TempDir()

	writeFile(t, filepath.Join(srcARoot, "CLAUDE.md"), "work rule\n")
	writeFile(t, filepath.Join(srcBRoot, "CLAUDE.md"), "personal rule\n")
	// Harness appended a new line at the very end.
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), "work rule\npersonal rule\nnew rule\n")

	srcs := []source.Source{newSource("work", srcARoot), newSource("personal", srcBRoot)}
	p := newProfile(srcs)
	m := &manifest.Manifest{
		Files:       map[string]string{"CLAUDE.md": "sha256:abc"},
		SourceFiles: map[string][]string{"CLAUDE.md": {"work", "personal"}},
	}
	c := watch.TargetChange{Root: targetRoot, Rel: "CLAUDE.md"}

	performed, err := writeBackMergedSource(m, c, p, srcs)
	if err != nil {
		t.Fatalf("writeBackMergedSource: %v", err)
	}
	if !performed {
		t.Fatal("expected performed=true")
	}

	// New line appended after personal rule → attributed to personal (last source before insertion).
	if got := readFile(t, filepath.Join(srcBRoot, "CLAUDE.md")); got != "personal rule\nnew rule\n" {
		t.Errorf("source B = %q, want %q", got, "personal rule\nnew rule\n")
	}
	if got := readFile(t, filepath.Join(srcARoot, "CLAUDE.md")); got != "work rule\n" {
		t.Errorf("source A unchanged = %q, want %q", got, "work rule\n")
	}
}
