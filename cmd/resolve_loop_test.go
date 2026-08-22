package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// The interactive loop (#260) adds no resolution semantics: every choice does
// what the equivalent flag does. What is tested here is the walking — that skip
// and quit leave conflicts held, and that settling one does not disturb the rest.

// divergeTwoSources edits two different sources' sections in both harnesses, so
// the walk has more than one conflict to step through.
func (w *resolveWorld) divergeTwoSources(t *testing.T) {
	t.Helper()
	for _, path := range []string{w.codex, w.windsurf} {
		body := readFile(t, path)
		body = strings.Replace(body, "# personal rules", "# personal rules\nfrom "+path, 1)
		body = strings.Replace(body, "# team rules", "# team rules\nfrom "+path, 1)
		writeFile(t, path, body)
	}
}

func runLoop(t *testing.T, w *resolveWorld, answers string) string {
	t.Helper()
	held, err := collectHeldConflicts(w.cfgDir, w.p.Name)
	if err != nil {
		t.Fatalf("collectHeldConflicts: %v", err)
	}
	out := &bytes.Buffer{}
	if lErr := resolveLoop(strings.NewReader(answers), out, w.cfgDir, w.p.Name, held); lErr != nil {
		t.Fatalf("resolveLoop: %v", lErr)
	}
	return out.String()
}

func TestResolveLoop_SkipLeavesTheConflictHeld(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)

	out := runLoop(t, w, "s\n")
	if !strings.Contains(out, "skipped") {
		t.Errorf("the loop did not report the skip:\n%s", out)
	}
	if got := w.heldLabels(t); len(got) != 1 {
		t.Errorf("skipping must leave the conflict held, still held: %v", got)
	}
}

func TestResolveLoop_QuitLeavesTheRemainderHeld(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeTwoSources(t)
	if got := w.heldLabels(t); len(got) != 2 {
		t.Fatalf("fixture should hold two conflicts, got %v", got)
	}

	runLoop(t, w, "q\n")
	if got := w.heldLabels(t); len(got) != 2 {
		t.Errorf("quitting must settle nothing, held: %v", got)
	}
}

func TestResolveLoop_TakeSettlesOnlyThatConflict(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeTwoSources(t)

	// "c" is codex's letter; "m", "d", "s" and "q" are reserved, so the first
	// free letter of "codex" is c and of "windsurf" is w.
	out := runLoop(t, w, "c\ns\n")
	if !strings.Contains(out, "resolved") {
		t.Errorf("the loop did not settle the first conflict:\n%s", out)
	}
	held := w.heldLabels(t)
	if len(held) != 1 {
		t.Fatalf("one settled and one skipped should leave one held, got %v", held)
	}
	// A conflict resolved mid-loop does not disturb the ones after it.
	if held[0] != instructionsPrefix+"team" {
		t.Errorf("the wrong conflict is still held: %v", held)
	}
}

func TestResolveLoop_DiffDoesNotSettleAnything(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)

	out := runLoop(t, w, "d\ns\n")
	if !strings.Contains(out, "what weft last wrote") {
		t.Errorf("the diff was not shown:\n%s", out)
	}
	if got := w.heldLabels(t); len(got) != 1 {
		t.Errorf("showing a diff must settle nothing, held: %v", got)
	}
}

func TestResolveLoop_UnknownChoiceRepromptsRatherThanActing(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)

	out := runLoop(t, w, "z\ns\n")
	if !strings.Contains(out, "not one of the choices") {
		t.Errorf("an unknown key should be rejected:\n%s", out)
	}
	if got := w.heldLabels(t); len(got) != 1 {
		t.Errorf("an unknown key must settle nothing, held: %v", got)
	}
}

func TestResolveWalk_NonInteractiveReportsAndFails(t *testing.T) {
	t.Setenv("CI", "1") // isInteractiveTTY treats CI as "nobody is watching"
	w := newResolveWorld(t)
	w.divergeApart(t)

	out := &bytes.Buffer{}
	err := runResolveWalk(strings.NewReader(""), out)
	if !errors.Is(err, errConflictsHeld) {
		t.Fatalf("expected a non-zero exit with conflicts held, got %v", err)
	}
	if !strings.Contains(out.String(), "! conflict: instructions:personal") {
		t.Errorf("the report did not name the held conflict:\n%s", out)
	}
	if got := w.heldLabels(t); len(got) != 1 {
		t.Errorf("reporting must settle nothing, held: %v", got)
	}
}

func TestResolveWalk_NothingHeldSaysSo(t *testing.T) {
	w := newResolveWorld(t)

	out := &bytes.Buffer{}
	if err := runResolveWalk(strings.NewReader(""), out); err != nil {
		t.Fatalf("runResolveWalk: %v", err)
	}
	if !strings.Contains(out.String(), "no conflicts held") {
		t.Errorf("expected a clean report, got:\n%s", out)
	}
	_ = w
}

// ── key binding ──────────────────────────────────────────────────────────────

func TestHarnessKeys_BindToTheNameAndAvoidReservedLetters(t *testing.T) {
	keys := harnessKeys([]string{"claude-code", "codex", "cursor"})
	seen := map[string]string{}
	for name, key := range keys {
		if key == keyMerge || key == keyDiff || key == keySkip || key == keyQuit {
			t.Errorf("%s took the reserved key %q", name, key)
		}
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s share the key %q", name, other, key)
		}
		seen[key] = name
		if !strings.Contains(strings.ToLower(name), key) {
			t.Errorf("%s bound to %q, which is not one of its letters", name, key)
		}
	}
	if len(keys) != 3 {
		t.Errorf("expected a key per harness, got %d", len(keys))
	}
}

func TestHarnessKeys_FallsBackToDigitsWhenLettersRunOut(t *testing.T) {
	// Every letter of "sm" is reserved (skip, merge), so no letter is available.
	keys := harnessKeys([]string{"sm"})
	if keys["sm"] != "1" {
		t.Errorf("expected a digit fallback, got %q", keys["sm"])
	}
}

func TestReportHeld_UsesTheApplyReportFormat(t *testing.T) {
	out := &bytes.Buffer{}
	reportHeld(out, []heldConflict{{
		Label:     "instructions:pers-tech",
		Harnesses: []string{"codex", "windsurf"},
		Since:     time.Date(2026, 8, 15, 9, 12, 0, 0, time.Local),
	}}, time.Date(2026, 8, 15, 18, 0, 0, 0, time.Local))

	want := "! conflict: instructions:pers-tech changed in codex and windsurf since 09:12\n" +
		"  → weft resolve instructions:pers-tech --take codex|windsurf\n"
	if out.String() != want {
		t.Errorf("report =\n%q\nwant\n%q", out.String(), want)
	}
}

// A flag with nothing to apply it to is a mistyped command. Falling through to
// the walk would start an interactive resolution nobody asked for.
func TestResolve_TakeWithoutATargetIsRejected(t *testing.T) {
	w := newResolveWorld(t)
	w.divergeApart(t)

	for _, flags := range []struct{ take, merged string }{
		{take: "merge"},
		{take: "codex"},
		{merged: "/tmp/whatever.md"},
	} {
		resolveTake, resolveMerged = flags.take, flags.merged
		err := resolveCmd.RunE(newHolderCmd(), nil)
		resolveTake, resolveMerged = "", ""
		if err == nil {
			t.Errorf("--take %q --merged %q with no target should be rejected", flags.take, flags.merged)
			continue
		}
		if !strings.Contains(err.Error(), "need the path") {
			t.Errorf("error should say what is missing, got: %v", err)
		}
	}
	// Nothing was settled by the rejected commands.
	if got := w.heldLabels(t); len(got) != 1 {
		t.Errorf("a rejected command must settle nothing, held: %v", got)
	}
}
