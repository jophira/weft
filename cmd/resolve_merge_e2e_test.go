package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jophira/weft/internal/merge"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

// End-to-end cover for `--take merge` (#258) and the interactive loop (#260),
// driven through the same entry points the command uses: a real apply into two
// Tier B harnesses, hand edits in both, then a resolution.

type resolveWorld struct {
	cfgDir   string
	srcs     []source.Source
	p        *profile.Profile
	codex    string // ~/.codex/AGENTS.md
	windsurf string // ~/.codeium/windsurf/global_rules.md
	personal string // the personal source's CLAUDE.md
}

// newResolveWorld applies the layered profile to codex and windsurf, so both
// hold the same managed block and both manifests record it.
func newResolveWorld(t *testing.T) *resolveWorld {
	t.Helper()
	base := withIsolatedConfig(t)

	srcs := buildLayeredSources(t)
	reg, err := newRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	for _, s := range srcs {
		if addErr := reg.Add(s); addErr != nil {
			t.Fatalf("add source %q: %v", s.Name, addErr)
		}
	}
	p := twoTierBTargets()
	pm, err := newProfileManager()
	if err != nil {
		t.Fatalf("profile manager: %v", err)
	}
	if cErr := pm.Create(*p); cErr != nil {
		t.Fatalf("create profile: %v", cErr)
	}
	activate(t, p.Name)

	if aErr := mergeAndApply(p, rootsOf(srcs), srcs, base, false); aErr != nil {
		t.Fatalf("initial mergeAndApply: %v", aErr)
	}
	return &resolveWorld{
		cfgDir:   base,
		srcs:     srcs,
		p:        p,
		codex:    filepath.Join(base, ".codex", "AGENTS.md"),
		windsurf: filepath.Join(base, ".codeium", "windsurf", "global_rules.md"),
		personal: filepath.Join(srcs[0].Root, "CLAUDE.md"),
	}
}

// divergeApart edits the personal section in both harnesses, in places far
// enough apart that a three-way merge keeps both.
func (w *resolveWorld) divergeApart(t *testing.T) {
	t.Helper()
	writeFile(t, w.codex, strings.Replace(readFile(t, w.codex),
		"# personal rules", "# personal rules\nbranch-naming rule", 1))
	writeFile(t, w.windsurf, strings.Replace(readFile(t, w.windsurf),
		"# personal rules", "test-naming rule\n# personal rules", 1))
}

// divergeOverlapping edits the same line in both harnesses, which is the case a
// merge cannot settle on its own.
func (w *resolveWorld) divergeOverlapping(t *testing.T) {
	t.Helper()
	writeFile(t, w.codex, strings.Replace(readFile(t, w.codex),
		"# personal rules", "# personal rules, per codex", 1))
	writeFile(t, w.windsurf, strings.Replace(readFile(t, w.windsurf),
		"# personal rules", "# personal rules, per windsurf", 1))
}

func (w *resolveWorld) held(t *testing.T) heldConflict {
	t.Helper()
	held, err := collectHeldConflicts(w.cfgDir, w.p.Name)
	if err != nil {
		t.Fatalf("collectHeldConflicts: %v", err)
	}
	h, err := findHeld(held, instructionsPrefix+"personal")
	if err != nil {
		t.Fatalf("finding the held instruction conflict: %v", err)
	}
	return h
}

func (w *resolveWorld) heldLabels(t *testing.T) []string {
	t.Helper()
	held, err := collectHeldConflicts(w.cfgDir, w.p.Name)
	if err != nil {
		t.Fatalf("collectHeldConflicts: %v", err)
	}
	labels := make([]string, len(held))
	for i, h := range held {
		labels[i] = h.Label
	}
	return labels
}

// stubEditor points $EDITOR at a command that either saves the file unchanged
// (touch) or exits without touching it (true), which is how a merge is accepted
// or abandoned.
func stubEditor(t *testing.T, cmd string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX editor stub on Windows")
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", cmd)
}

// ── merge ────────────────────────────────────────────────────────────────────

func TestResolveMerge_NonOverlappingEditsKeepBoth(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)
	stubEditor(t, "touch")

	h := w.held(t)
	merged, err := mergeAndReview(&bytes.Buffer{}, w.cfgDir, h, time.Now())
	if err != nil {
		t.Fatalf("mergeAndReview: %v", err)
	}
	if sErr := settleHeld(&bytes.Buffer{}, h, mergeWinner, merged); sErr != nil {
		t.Fatalf("settleHeld: %v", sErr)
	}

	src := readFile(t, w.personal)
	for _, want := range []string{"branch-naming rule", "test-naming rule"} {
		if !strings.Contains(src, want) {
			t.Errorf("merge dropped %q from the source:\n%s", want, src)
		}
	}
	// Both harnesses converge on the merged text, so the next apply is quiet.
	for _, path := range []string{w.codex, w.windsurf} {
		got := readFile(t, path)
		for _, want := range []string{"branch-naming rule", "test-naming rule"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s missing %q after the merge:\n%s", path, want, got)
			}
		}
	}
}

func TestResolveMerge_CleanMergeStillOpensTheEditor(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)
	stubEditor(t, "true") // opens, exits, writes nothing

	h := w.held(t)
	_, err := mergeAndReview(&bytes.Buffer{}, w.cfgDir, h, time.Now())
	if !errors.Is(err, errMergeAbandoned) {
		t.Fatalf("expected the merge to be abandoned, got %v", err)
	}
	// Nothing reaches a source or a harness until the editor closes on a save.
	src := readFile(t, w.personal)
	if strings.Contains(src, "branch-naming rule") || strings.Contains(src, "test-naming rule") {
		t.Errorf("an abandoned merge wrote to the source:\n%s", src)
	}
	if !strings.Contains(readFile(t, w.codex), "branch-naming rule") {
		t.Error("codex lost its own edit on an abandoned merge")
	}
	if !strings.Contains(readFile(t, w.windsurf), "test-naming rule") {
		t.Error("windsurf lost its own edit on an abandoned merge")
	}
}

func TestResolveMerge_OverlappingEditsAreMarkedWithHarnessNames(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeOverlapping(t)
	stubEditor(t, "true")

	h := w.held(t)
	if _, err := mergeAndReview(&bytes.Buffer{}, w.cfgDir, h, time.Now()); !errors.Is(err, errMergeAbandoned) {
		t.Fatalf("expected the merge to be abandoned, got %v", err)
	}

	work := readFile(t, mergeWorkFile(w.cfgDir, h.Label))
	if !merge.HasConflictMarkers(work) {
		t.Fatalf("overlapping edits must be marked in the work file:\n%s", work)
	}
	for _, want := range []string{"<<<<<<< codex", ">>>>>>> windsurf"} {
		if !strings.Contains(work, want) {
			t.Errorf("work file missing %q:\n%s", want, work)
		}
	}
	// The hazard git does not have: a marker in a harness path is live model
	// input on the next turn.
	for _, path := range []string{w.codex, w.windsurf, w.personal} {
		if merge.HasConflictMarkers(readFile(t, path)) {
			t.Errorf("conflict markers reached %s", path)
		}
	}
}

func TestResolveMerge_SavedMarkersAreRefused(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)

	h := w.held(t)
	marked := "<<<<<<< codex\na\n=======\nb\n>>>>>>> windsurf\n"
	err := settleHeld(&bytes.Buffer{}, h, mergeWinner, []byte(marked))
	if err == nil {
		t.Fatal("expected marker text to be refused")
	}
	for _, path := range []string{w.codex, w.windsurf, w.personal} {
		if merge.HasConflictMarkers(readFile(t, path)) {
			t.Errorf("conflict markers reached %s", path)
		}
	}
}

func TestResolveMerge_ProvenanceHeaderIsStrippedOnSave(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)
	stubEditor(t, "touch")

	h := w.held(t)
	merged, err := mergeAndReview(&bytes.Buffer{}, w.cfgDir, h, time.Now())
	if err != nil {
		t.Fatalf("mergeAndReview: %v", err)
	}
	if strings.Contains(string(merged), mergeHeaderBegin) {
		t.Errorf("the provenance header survived into the resolution:\n%s", merged)
	}
	// It was there to be read, though.
	if !strings.Contains(readFile(t, mergeWorkFile(w.cfgDir, h.Label)), mergeHeaderBegin) {
		t.Error("the work file carried no provenance header")
	}
}

func TestResolveMerge_NonTTYIsRefused(t *testing.T) {
	t.Setenv("CI", "1") // isInteractiveTTY treats CI as "nobody is watching"
	w := newResolveWorld(t)
	w.divergeApart(t)

	err := runResolveConflict(&bytes.Buffer{}, instructionsPrefix+"personal", "merge", "")
	if !errors.Is(err, errMergeNeedsTTY) {
		t.Fatalf("expected --take merge to refuse an unattended run, got %v", err)
	}
	if strings.Contains(readFile(t, w.personal), "branch-naming rule") {
		t.Error("a refused merge wrote to the source")
	}
}

func TestResolveMerge_UnstampedReviewedFileIsTrusted(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)

	// The path taken when $EDITOR is unset: weft writes the merge, the user
	// reviews it themselves, and --merged consumes the result. Text assembled by
	// hand carries no weft:merge:from stamp and is taken at face value — refusing
	// what someone wrote themselves would be worse than trusting it.
	h := w.held(t)
	base, sides := h.Base, make([]merge.Side, 0, len(h.Harnesses))
	for _, name := range sortedHarnesses(h) {
		sides = append(sides, merge.Side{Label: name, Text: h.Sides[name]})
	}
	merged, _ := merge.ThreeWay(base, sides)
	reviewed := filepath.Join(t.TempDir(), "reviewed.md")
	writeFile(t, reviewed, merged)

	if err := runResolveConflict(&bytes.Buffer{}, instructionsPrefix+"personal", "", reviewed); err != nil {
		t.Fatalf("runResolveConflict --merged: %v", err)
	}
	src := readFile(t, w.personal)
	for _, want := range []string{"branch-naming rule", "test-naming rule"} {
		if !strings.Contains(src, want) {
			t.Errorf("--merged dropped %q from the source:\n%s", want, src)
		}
	}
}

// ── --take on an instruction section ─────────────────────────────────────────

func TestResolveTake_InstructionSectionPicksOneSide(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)

	out := &bytes.Buffer{}
	if err := runResolveConflict(out, instructionsPrefix+"personal", "codex", ""); err != nil {
		t.Fatalf("runResolveConflict --take codex: %v", err)
	}
	src := readFile(t, w.personal)
	if !strings.Contains(src, "branch-naming rule") {
		t.Errorf("the winning edit did not reach the source:\n%s", src)
	}
	if strings.Contains(src, "test-naming rule") {
		t.Errorf("the losing edit reached the source:\n%s", src)
	}
	// The loser is rewritten from the winner, and kept first.
	if !strings.Contains(readFile(t, w.windsurf), "branch-naming rule") {
		t.Error("windsurf was not brought into line with the winner")
	}
	backup := filepath.Join(w.cfgDir, "backups", "resolve")
	entries, err := os.ReadDir(backup)
	if err != nil || len(entries) == 0 {
		t.Fatalf("the losing copy was not backed up under %s (%v)", backup, err)
	}
}

func TestResolveTake_UnknownHarnessNamesTheChoices(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)

	err := runResolveConflict(&bytes.Buffer{}, instructionsPrefix+"personal", "aider", "")
	if err == nil || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("expected the error to name the real choices, got %v", err)
	}
}

func TestResolve_ConflictIsSettledOnceAndThenGone(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)

	if err := runResolveConflict(&bytes.Buffer{}, instructionsPrefix+"personal", "codex", ""); err != nil {
		t.Fatalf("runResolveConflict: %v", err)
	}
	held, err := collectHeldConflicts(w.cfgDir, w.p.Name)
	if err != nil {
		t.Fatalf("collectHeldConflicts: %v", err)
	}
	for _, h := range held {
		if h.Label == instructionsPrefix+"personal" {
			t.Errorf("the conflict is still held after being settled: %+v", h.Harnesses)
		}
	}
}

// ── the file class, through the same command path ────────────────────────────

// newFileConflictWorld applies to claude-code and codex, which both carry
// commands/, so a hand edit in each is a file conflict rather than an
// instruction one. Codex relocates commands to prompts/, which is the point:
// the two paths do not look alike.
func newFileConflictWorld(t *testing.T) (cfgDir, claudeCmd, codexCmd string) {
	t.Helper()
	base := withIsolatedConfig(t)

	srcs := buildLayeredSources(t)
	reg, err := newRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	for _, s := range srcs {
		if addErr := reg.Add(s); addErr != nil {
			t.Fatalf("add source %q: %v", s.Name, addErr)
		}
	}
	p := &profile.Profile{
		Name:    "layered",
		Sources: []string{"personal", "team", "company"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"claude-code", "codex"},
	}
	pm, err := newProfileManager()
	if err != nil {
		t.Fatalf("profile manager: %v", err)
	}
	if cErr := pm.Create(*p); cErr != nil {
		t.Fatalf("create profile: %v", cErr)
	}
	activate(t, p.Name)
	if aErr := mergeAndApply(p, rootsOf(srcs), srcs, base, false); aErr != nil {
		t.Fatalf("initial mergeAndApply: %v", aErr)
	}
	return base,
		filepath.Join(base, ".claude", "commands", "hello.md"),
		filepath.Join(base, ".codex", "prompts", "hello.md")
}

func TestResolveMerge_FileConflictKeepsBothEdits(t *testing.T) {
	cfgDir, claudeCmd, codexCmd := newFileConflictWorld(t)
	stubEditor(t, "touch")

	writeFile(t, claudeCmd, "from claude\nsay hello\n")
	writeFile(t, codexCmd, "say hello\nfrom codex\n")

	held, err := collectHeldConflicts(cfgDir, "layered")
	if err != nil {
		t.Fatalf("collectHeldConflicts: %v", err)
	}
	h, err := findHeld(held, "commands/hello.md")
	if err != nil {
		t.Fatalf("finding the held file conflict: %v", err)
	}
	if h.Base != "say hello" {
		t.Fatalf("base should be the staged copy, got %q", h.Base)
	}

	merged, err := mergeAndReview(&bytes.Buffer{}, cfgDir, h, time.Now())
	if err != nil {
		t.Fatalf("mergeAndReview: %v", err)
	}
	if sErr := settleHeld(&bytes.Buffer{}, h, mergeWinner, merged); sErr != nil {
		t.Fatalf("settleHeld: %v", sErr)
	}
	for _, path := range []string{claudeCmd, codexCmd} {
		got := readFile(t, path)
		for _, want := range []string{"from claude", "from codex"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s missing %q after the merge:\n%s", path, want, got)
			}
		}
	}
}

// ── review-window safety and flag validation ─────────────────────────────────

// An editor session has no bound. A harness writing to the same section while
// the merge is open must not have that write overwritten by the reviewed text.
func TestResolveMerge_EditDuringReviewAbortsTheResolution(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)
	// An editor that edits the *harness* copy, standing in for another tool
	// writing to it while the user is reading the merge.
	stubEditor(t, "true")

	h := w.held(t)
	writeFile(t, w.codex, strings.Replace(readFile(t, w.codex),
		"branch-naming rule", "branch-naming rule, revised mid-review", 1))

	if err := confirmUnchanged(w.cfgDir, w.p.Name, h); err == nil {
		t.Fatal("expected the stale snapshot to be rejected")
	}
	// The mid-review edit is still there, and so is the conflict.
	if !strings.Contains(readFile(t, w.codex), "revised mid-review") {
		t.Error("the mid-review edit was lost")
	}
	if got := w.heldLabels(t); len(got) != 1 {
		t.Errorf("the conflict should still be held, got %v", got)
	}
}

func TestResolveMerge_UntouchedConflictPassesTheRecheck(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)

	h := w.held(t)
	if err := confirmUnchanged(w.cfgDir, w.p.Name, h); err != nil {
		t.Fatalf("an untouched conflict must pass the recheck: %v", err)
	}
}

func TestResolveMerge_StaleReviewedFileIsRejected(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)
	stubEditor(t, "touch")

	// Prepare a merge, which stamps the work file with what it was merged from.
	h := w.held(t)
	if _, err := mergeAndReview(&bytes.Buffer{}, w.cfgDir, h, time.Now()); err != nil {
		t.Fatalf("mergeAndReview: %v", err)
	}
	work := mergeWorkFile(w.cfgDir, h.Label)

	// The world moves on between the review and the --merged that consumes it.
	writeFile(t, w.windsurf, strings.Replace(readFile(t, w.windsurf),
		"test-naming rule", "test-naming rule, revised later", 1))

	err := runResolveConflict(&bytes.Buffer{}, instructionsPrefix+"personal", "", work)
	if err == nil {
		t.Fatal("expected a stale reviewed file to be rejected")
	}
	if !strings.Contains(readFile(t, w.windsurf), "revised later") {
		t.Error("the later edit was overwritten by a stale review")
	}
}

func TestResolveMerge_CurrentReviewedFileIsAccepted(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)
	stubEditor(t, "touch")

	h := w.held(t)
	if _, err := mergeAndReview(&bytes.Buffer{}, w.cfgDir, h, time.Now()); err != nil {
		t.Fatalf("mergeAndReview: %v", err)
	}
	if err := runResolveConflict(&bytes.Buffer{}, instructionsPrefix+"personal", "",
		mergeWorkFile(w.cfgDir, h.Label)); err != nil {
		t.Fatalf("an unchanged conflict must accept its own reviewed file: %v", err)
	}
	src := readFile(t, w.personal)
	for _, want := range []string{"branch-naming rule", "test-naming rule"} {
		if !strings.Contains(src, want) {
			t.Errorf("--merged dropped %q from the source:\n%s", want, src)
		}
	}
}

func TestResolveMerge_EmptyBaseIsStillAMergeableBase(t *testing.T) {
	cfgDir, claudeCmd, codexCmd := newFileConflictWorld(t)

	// hello.md staged as empty is a real base: weft wrote it, and it says the
	// file had no content. Two additions to it can still be merged.
	staged := filepath.Join(cfgDir, "staged", "layered", "commands", "hello.md")
	writeFile(t, staged, "")
	writeFile(t, claudeCmd, "from claude\n")
	writeFile(t, codexCmd, "from codex\n")

	held, err := collectHeldConflicts(cfgDir, "layered")
	if err != nil {
		t.Fatalf("collectHeldConflicts: %v", err)
	}
	h, err := findHeld(held, "commands/hello.md")
	if err != nil {
		t.Fatalf("finding the held file conflict: %v", err)
	}
	if h.Base != "" || !h.HasBase {
		t.Fatalf("an empty staged file is an empty base that exists, got %q / HasBase=%v", h.Base, h.HasBase)
	}
	stubEditor(t, "touch")
	if _, mErr := mergeAndReview(&bytes.Buffer{}, cfgDir, h, time.Now()); errors.Is(mErr, errNoMergeBase) {
		t.Error("an empty base was mistaken for a missing one")
	}
}

func TestResolveMerge_MissingBaseRefusesTheMerge(t *testing.T) {
	cfgDir, claudeCmd, codexCmd := newFileConflictWorld(t)

	writeFile(t, claudeCmd, "from claude\n")
	writeFile(t, codexCmd, "from codex\n")
	held, err := collectHeldConflicts(cfgDir, "layered")
	if err != nil {
		t.Fatalf("collectHeldConflicts: %v", err)
	}
	h, err := findHeld(held, "commands/hello.md")
	if err != nil {
		t.Fatalf("finding the held file conflict: %v", err)
	}
	h.HasBase, h.Base = false, "" // as after a profile switch

	if _, mErr := mergeAndReview(&bytes.Buffer{}, cfgDir, h, time.Now()); !errors.Is(mErr, errNoMergeBase) {
		t.Fatalf("expected a missing base to refuse the merge, got %v", mErr)
	}
}
