package harness

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/testenv"
)

// conflictFixture stages one command and projects it into two harness roots:
// Claude Code, which keeps commands/, and Codex, which relocates them to
// prompts/. The routed pair is the point — a conflict is between two paths that
// do not look alike, so detection has to go through the class model rather than
// compare relative paths.
type conflictFixture struct {
	staged     string
	cfgDir     string
	claudeRoot string
	codexRoot  string
	appliedAt  time.Time
}

const (
	conflictRel        = "commands/review.md"
	conflictCodexRel   = "prompts/review.md"
	conflictOriginal   = "# review\noriginal\n"
	conflictClaudeEdit = "# review\nedited in claude-code\n"
	conflictCodexEdit  = "# review\nedited in codex\n"
)

func newConflictFixture(t *testing.T) *conflictFixture {
	t.Helper()
	base := t.TempDir()
	f := &conflictFixture{
		staged:     filepath.Join(base, "staged"),
		cfgDir:     filepath.Join(base, "cfg"),
		claudeRoot: filepath.Join(base, "claude"),
		codexRoot:  filepath.Join(base, "codex"),
		appliedAt:  time.Date(2026, 8, 15, 14, 2, 0, 0, time.Local),
	}
	write(t, filepath.Join(f.staged, filepath.FromSlash(conflictRel)), conflictOriginal)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictOriginal)
	write(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)), conflictOriginal)

	hash := manifest.HashBytes([]byte(conflictOriginal))
	f.saveManifest(t, "claude-code", f.claudeRoot, filepath.FromSlash(conflictRel), hash)
	f.saveManifest(t, "codex", f.codexRoot, filepath.FromSlash(conflictCodexRel), hash)
	return f
}

func (f *conflictFixture) saveManifest(t *testing.T, name, root, rel, hash string) {
	t.Helper()
	m := &manifest.Manifest{
		Harness:    name,
		Profile:    "test",
		TargetRoot: root,
		AppliedAt:  f.appliedAt,
		Files:      map[string]string{rel: hash},
		Staged:     []string{rel},
	}
	if err := manifest.Save(f.cfgDir, m); err != nil {
		t.Fatalf("saving %s manifest: %v", name, err)
	}
}

func (f *conflictFixture) targets(t *testing.T) []ConflictTarget {
	t.Helper()
	load := func(name string) *manifest.Manifest {
		m, err := manifest.Load(f.cfgDir, name)
		if err != nil {
			t.Fatalf("loading %s manifest: %v", name, err)
		}
		return m
	}
	return []ConflictTarget{
		{Harness: "claude-code", Root: f.claudeRoot, H: &ClaudeCode{}, Manifest: load("claude-code")},
		{Harness: "codex", Root: f.codexRoot, H: &Codex{}, Manifest: load("codex")},
	}
}

func (f *conflictFixture) detect(t *testing.T) []Conflict {
	t.Helper()
	got, err := DetectConflicts(f.staged, f.targets(t))
	if err != nil {
		t.Fatalf("DetectConflicts: %v", err)
	}
	return got
}

func TestDetectConflicts_NoDivergence(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)

	if got := f.detect(t); len(got) != 0 {
		t.Errorf("expected no conflicts when both copies match the manifest, got %+v", got)
	}
}

// One diverged copy is the ordinary write-back case. Reporting it as a conflict
// would make every single-harness edit demand a manual decision.
func TestDetectConflicts_SingleDivergenceIsWriteBack(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictClaudeEdit)

	if got := f.detect(t); len(got) != 0 {
		t.Errorf("one diverged copy is not a conflict, got %+v", got)
	}
}

func TestDetectConflicts_TwoDivergedTargets(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictClaudeEdit)
	write(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)), conflictCodexEdit)

	got := f.detect(t)
	if len(got) != 1 {
		t.Fatalf("expected exactly one conflict, got %+v", got)
	}
	c := got[0]
	if c.Canonical != conflictRel {
		t.Errorf("canonical path = %q, want %q", c.Canonical, conflictRel)
	}
	if names := strings.Join(c.Harnesses(), ","); names != "claude-code,codex" {
		t.Errorf("diverged harnesses = %q, want claude-code,codex", names)
	}
	if !c.Since.Equal(f.appliedAt) {
		t.Errorf("Since = %v, want the recorded apply time %v", c.Since, f.appliedAt)
	}
	// The Codex side must be named by its routed path, not the staged one.
	for _, d := range c.Diverged {
		if d.Harness == "codex" && d.Rel != filepath.FromSlash(conflictCodexRel) {
			t.Errorf("codex divergence rel = %q, want %q", d.Rel, conflictCodexRel)
		}
	}
}

// An unowned file has no recorded hash to differ from, so it cannot contribute
// to a conflict — apply already treats it as an external file and backs it up.
func TestDetectConflicts_UnownedFileIsNotDiverged(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictClaudeEdit)
	write(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)), conflictCodexEdit)

	m, err := manifest.Load(f.cfgDir, "codex")
	if err != nil {
		t.Fatalf("loading codex manifest: %v", err)
	}
	delete(m.Files, filepath.FromSlash(conflictCodexRel))
	if err := manifest.Save(f.cfgDir, m); err != nil {
		t.Fatalf("saving codex manifest: %v", err)
	}

	if got := f.detect(t); len(got) != 0 {
		t.Errorf("an unowned copy must not count as diverged, got %+v", got)
	}
}

func TestConflict_ReportNamesHarnessesAndTime(t *testing.T) {
	c := Conflict{
		Canonical: conflictRel,
		Diverged: []Divergence{
			{Harness: "claude-code"},
			{Harness: "codex"},
		},
		Since: time.Date(2026, 8, 15, 14, 2, 0, 0, time.Local),
	}
	got := c.Report(time.Date(2026, 8, 15, 17, 30, 0, 0, time.Local))

	want := "! conflict: commands/review.md changed in claude-code and codex since 14:02\n" +
		"  → weft resolve commands/review.md --take claude-code|codex\n"
	if got != want {
		t.Errorf("report =\n%q\nwant\n%q", got, want)
	}
}

// An apply from last week must not be rendered as a bare clock time, which the
// reader would take for today.
func TestConflict_ReportDatesAnOlderApply(t *testing.T) {
	c := Conflict{
		Canonical: conflictRel,
		Diverged:  []Divergence{{Harness: "a"}, {Harness: "b"}},
		Since:     time.Date(2026, 8, 8, 9, 15, 0, 0, time.Local),
	}
	got := c.Report(time.Date(2026, 8, 15, 17, 30, 0, 0, time.Local))

	if !strings.Contains(got, "since 2026-08-08 09:15") {
		t.Errorf("older apply must be dated, got %q", got)
	}
}

// The refusal is the whole feature: after an apply that holds the file, both
// copies must be byte-identical to what the user left there.
func TestApplyWithManifest_HeldConflictLeavesBothCopiesUntouched(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictClaudeEdit)
	write(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)), conflictCodexEdit)

	held := HeldPaths(f.detect(t))
	buf := &bytes.Buffer{}
	ctx := ApplyCtx{
		ProfileName: "test", CfgDir: f.cfgDir, Out: buf, Held: held["claude-code"],
	}
	if err := applyWithManifest(f.staged, f.claudeRoot, "claude-code", ctx, nil, nil, &ClaudeCode{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := readFile(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel))); got != conflictClaudeEdit {
		t.Errorf("held claude-code copy was rewritten: %q", got)
	}
	if got := readFile(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel))); got != conflictCodexEdit {
		t.Errorf("codex copy was touched by the claude-code apply: %q", got)
	}
	if !strings.Contains(buf.String(), "! held") {
		t.Errorf("apply log must report the hold, got %q", buf.String())
	}
	if entries, err := os.ReadDir(filepath.Join(f.cfgDir, "backups")); err == nil && len(entries) > 0 {
		t.Errorf("a held file must not be backed up — the user's copy stays where they put it")
	}
	// The recorded hash must stay at what weft last wrote, or the next apply would
	// see the two copies as reconciled and the conflict would disappear.
	m, err := manifest.Load(f.cfgDir, "claude-code")
	if err != nil {
		t.Fatalf("loading manifest: %v", err)
	}
	if m.Files[filepath.FromSlash(conflictRel)] != manifest.HashBytes([]byte(conflictOriginal)) {
		t.Errorf("held file's manifest hash changed to %q", m.Files[filepath.FromSlash(conflictRel)])
	}
	if len(f.detect(t)) != 1 {
		t.Errorf("the conflict must survive an apply that held it")
	}
}

// ── weft resolve --take ──────────────────────────────────────────────────────

func TestResolve_TakeRewritesTheLosingCopy(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictClaudeEdit)
	write(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)), conflictCodexEdit)
	srcPath := filepath.Join(t.TempDir(), "commands", "review.md")

	conflicts := f.detect(t)
	res, err := Resolve(ResolveRequest{
		Conflict: conflicts[0], Take: "claude-code", SourcePath: srcPath, CfgDir: f.cfgDir,
		SourceContent: markSourceWrite,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if res.Winner != "claude-code" {
		t.Errorf("winner = %q, want claude-code", res.Winner)
	}
	if got := readFile(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel))); got != conflictClaudeEdit {
		t.Errorf("losing codex copy = %q, want the winner's content", got)
	}
	if got := readFile(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel))); got != conflictClaudeEdit {
		t.Errorf("winning copy was modified: %q", got)
	}
	// The source gets the normalised form, the harness copies the raw one.
	if got := readFile(t, srcPath); got != sourceMark+conflictClaudeEdit {
		t.Errorf("source = %q, want the winner's content passed through SourceContent", got)
	}
	// Losing text is never simply gone: the user chose a winner, not the deletion
	// of the other version.
	backup := filepath.Join(res.BackupDir, "codex", filepath.FromSlash(conflictCodexRel))
	if got := readFile(t, backup); got != conflictCodexEdit {
		t.Errorf("backup of the codex copy = %q, want its pre-resolution content", got)
	}
	if len(f.detect(t)) != 0 {
		t.Errorf("the conflict must be gone once resolved")
	}
}

// The losing file must still be there afterwards. Deleting it and letting the
// next apply recreate it would lose everything between the two moments.
func TestResolve_LosingFileIsNeverDropped(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictClaudeEdit)
	write(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)), conflictCodexEdit)

	conflicts := f.detect(t)
	if _, err := Resolve(ResolveRequest{Conflict: conflicts[0], Take: "codex", CfgDir: f.cfgDir}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	for _, p := range []string{
		filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)),
		filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must still exist after resolution: %v", p, err)
		}
	}
	if got := readFile(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel))); got != conflictCodexEdit {
		t.Errorf("claude-code copy = %q, want the codex winner's content", got)
	}
}

func TestResolve_MergeIsRefused(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictClaudeEdit)
	write(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)), conflictCodexEdit)

	_, err := Resolve(ResolveRequest{Conflict: f.detect(t)[0], Take: "merge", CfgDir: f.cfgDir})
	if !errors.Is(err, ErrTakeMerge) {
		t.Fatalf("expected ErrTakeMerge, got %v", err)
	}
	if got := readFile(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel))); got != conflictCodexEdit {
		t.Errorf("a refused resolve must not write anything, codex copy = %q", got)
	}
}

func TestResolve_UnknownHarnessNamesTheChoices(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictClaudeEdit)
	write(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)), conflictCodexEdit)

	_, err := Resolve(ResolveRequest{Conflict: f.detect(t)[0], Take: "cursor", CfgDir: f.cfgDir})
	if err == nil {
		t.Fatal("expected an error for a harness not in the conflict")
	}
	if !strings.Contains(err.Error(), "claude-code, codex") {
		t.Errorf("error must list the real choices, got %v", err)
	}
}

// ── merged resolution (#258) ─────────────────────────────────────────────────

func TestResolve_MergedContentReplacesEveryCopy(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictClaudeEdit)
	write(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)), conflictCodexEdit)

	merged := "# review\nedited in claude-code\nedited in codex\n"
	src := filepath.Join(t.TempDir(), "review.md")
	res, err := Resolve(ResolveRequest{
		Conflict: f.detect(t)[0], Merged: []byte(merged), SourcePath: src, CfgDir: f.cfgDir,
		SourceContent: markSourceWrite,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// A merge has no winner, so no copy is left holding its own version.
	if res.Winner != "merge" {
		t.Errorf("Winner = %q, want %q", res.Winner, "merge")
	}
	if len(res.Rewritten) != 2 {
		t.Errorf("a merge rewrites every diverged copy, got %d", len(res.Rewritten))
	}
	for _, p := range []string{
		filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)),
		filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)),
	} {
		if got := readFile(t, p); got != merged {
			t.Errorf("%s = %q, want the merged text", p, got)
		}
	}
	// The merged branch normalises for the source too — the reviewed text was
	// read out of a harness, so it carries the harness-side expansions.
	if got := readFile(t, src); got != sourceMark+merged {
		t.Errorf("source = %q, want the merged text passed through SourceContent", got)
	}
	// Both copies were replaced by text neither held, so both were backed up.
	for _, name := range []string{"claude-code", "codex"} {
		if _, err := os.Stat(filepath.Join(res.BackupDir, name)); err != nil {
			t.Errorf("no backup for %s: %v", name, err)
		}
	}
}

func TestResolve_MergedContentWithMarkersIsRefused(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictClaudeEdit)
	write(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)), conflictCodexEdit)

	marked := "# review\n<<<<<<< claude-code\na\n=======\nb\n>>>>>>> codex\n"
	_, err := Resolve(ResolveRequest{Conflict: f.detect(t)[0], Merged: []byte(marked), CfgDir: f.cfgDir})
	if !errors.Is(err, ErrConflictMarkers) {
		t.Fatalf("expected ErrConflictMarkers, got %v", err)
	}
	// A harness instruction file is live model input: a marker left there is read
	// as instructions on the next turn, so a refused write leaves both copies be.
	if got := readFile(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel))); got != conflictCodexEdit {
		t.Errorf("a refused merge must not write anything, codex copy = %q", got)
	}
}

// ── the source write is normalised, never raw (#285) ─────────────────────────

const sourceMark = "NORMALISED\n"

// markSourceWrite stands in for the write-back normalisation package main
// supplies. A visible prefix is enough to prove Resolve routed the source write
// through the hook and the harness writes around it.
func markSourceWrite(b []byte) []byte { return append([]byte(sourceMark), b...) }

// Naming a SourcePath without a SourceContent is refused, and refused before
// anything is written — a half-settled conflict is worse than an unsettled one.
func TestResolve_SourcePathWithoutSourceContentIsRefused(t *testing.T) {
	testenv.ClearPath(t)
	f := newConflictFixture(t)
	write(t, filepath.Join(f.claudeRoot, filepath.FromSlash(conflictRel)), conflictClaudeEdit)
	write(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel)), conflictCodexEdit)
	srcPath := filepath.Join(t.TempDir(), "review.md")

	_, err := Resolve(ResolveRequest{
		Conflict: f.detect(t)[0], Take: "claude-code", SourcePath: srcPath, CfgDir: f.cfgDir,
	})
	if !errors.Is(err, ErrNoSourceContent) {
		t.Fatalf("err = %v, want ErrNoSourceContent", err)
	}
	// Nothing was written: the losing copy still holds its own edit.
	if got := readFile(t, filepath.Join(f.codexRoot, filepath.FromSlash(conflictCodexRel))); got != conflictCodexEdit {
		t.Errorf("codex copy was modified before the guard fired: %q", got)
	}
	if _, sErr := os.Stat(srcPath); sErr == nil {
		t.Error("the source file was written despite the guard")
	}
}
