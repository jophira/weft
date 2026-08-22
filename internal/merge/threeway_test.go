package merge_test

import (
	"strings"
	"testing"

	"github.com/jophira/weft/internal/merge"
)

const mergeBase = "one\ntwo\nthree\nfour\nfive\n"

func TestThreeWay_NonOverlappingEditsKeepBoth(t *testing.T) {
	ours := "one\ntwo EDITED\nthree\nfour\nfive\n"
	theirs := "one\ntwo\nthree\nfour\nfive EDITED\n"

	got, conflicted := merge.ThreeWay(mergeBase, []merge.Side{
		{Label: "codex", Text: ours},
		{Label: "windsurf", Text: theirs},
	})
	if conflicted {
		t.Fatalf("edits in different places must merge cleanly, got:\n%s", got)
	}
	want := "one\ntwo EDITED\nthree\nfour\nfive EDITED\n"
	if got != want {
		t.Errorf("merged = %q, want %q", got, want)
	}
}

func TestThreeWay_AdditionsFromBothSidesSurvive(t *testing.T) {
	ours := "one\ntwo\nthree\nfour\nfive\nbranch naming rule\n"
	theirs := "test naming rule\none\ntwo\nthree\nfour\nfive\n"

	got, conflicted := merge.ThreeWay(mergeBase, []merge.Side{
		{Label: "codex", Text: ours},
		{Label: "windsurf", Text: theirs},
	})
	if conflicted {
		t.Fatalf("appends at opposite ends must merge cleanly, got:\n%s", got)
	}
	if !strings.Contains(got, "branch naming rule") || !strings.Contains(got, "test naming rule") {
		t.Errorf("merged text dropped an edit:\n%s", got)
	}
}

func TestThreeWay_OverlappingEditsAreMarkedWithHarnessNames(t *testing.T) {
	ours := "one\ntwo FROM CODEX\nthree\nfour\nfive\n"
	theirs := "one\ntwo FROM WINDSURF\nthree\nfour\nfive\n"

	got, conflicted := merge.ThreeWay(mergeBase, []merge.Side{
		{Label: "codex", Text: ours},
		{Label: "windsurf", Text: theirs},
	})
	if !conflicted {
		t.Fatalf("edits to the same line must conflict, got:\n%s", got)
	}
	if !merge.HasConflictMarkers(got) {
		t.Fatalf("conflicted merge must carry markers:\n%s", got)
	}
	for _, want := range []string{"<<<<<<< codex", "=======", ">>>>>>> windsurf", "two FROM CODEX", "two FROM WINDSURF"} {
		if !strings.Contains(got, want) {
			t.Errorf("merged text missing %q:\n%s", want, got)
		}
	}
	// Untouched lines stay outside the marked region.
	if !strings.HasPrefix(got, "one\n") || !strings.HasSuffix(got, "five\n") {
		t.Errorf("unchanged context was swallowed by the conflict region:\n%s", got)
	}
}

func TestThreeWay_IdenticalEditsOnBothSidesDoNotConflict(t *testing.T) {
	same := "one\ntwo SAME\nthree\nfour\nfive\n"
	got, conflicted := merge.ThreeWay(mergeBase, []merge.Side{
		{Label: "codex", Text: same},
		{Label: "windsurf", Text: same},
	})
	if conflicted {
		t.Fatalf("identical edits must not conflict, got:\n%s", got)
	}
	if got != same {
		t.Errorf("merged = %q, want %q", got, same)
	}
}

func TestThreeWay_ThreeSidesFold(t *testing.T) {
	a := "one EDITED\ntwo\nthree\nfour\nfive\n"
	b := "one\ntwo\nthree EDITED\nfour\nfive\n"
	c := "one\ntwo\nthree\nfour\nfive EDITED\n"

	got, conflicted := merge.ThreeWay(mergeBase, []merge.Side{
		{Label: "aider", Text: a},
		{Label: "codex", Text: b},
		{Label: "windsurf", Text: c},
	})
	if conflicted {
		t.Fatalf("three disjoint edits must merge cleanly, got:\n%s", got)
	}
	want := "one EDITED\ntwo\nthree EDITED\nfour\nfive EDITED\n"
	if got != want {
		t.Errorf("merged = %q, want %q", got, want)
	}
}

func TestThreeWay_DeletionOnOneSideIsKept(t *testing.T) {
	ours := "one\nthree\nfour\nfive\n"         // dropped "two"
	theirs := "one\ntwo\nthree\nfour\nfive!\n" // edited the last line

	got, conflicted := merge.ThreeWay(mergeBase, []merge.Side{
		{Label: "codex", Text: ours},
		{Label: "windsurf", Text: theirs},
	})
	if conflicted {
		t.Fatalf("a deletion away from the other edit must merge cleanly, got:\n%s", got)
	}
	if strings.Contains(got, "two\n") {
		t.Errorf("deleted line came back:\n%s", got)
	}
	if !strings.Contains(got, "five!") {
		t.Errorf("other side's edit was lost:\n%s", got)
	}
}

func TestThreeWay_SingleSideIsReturnedUnchanged(t *testing.T) {
	got, conflicted := merge.ThreeWay(mergeBase, []merge.Side{{Label: "codex", Text: "only\n"}})
	if conflicted || got != "only\n" {
		t.Errorf("ThreeWay(1 side) = %q, %v; want %q, false", got, conflicted, "only\n")
	}
}

func TestHasConflictMarkers(t *testing.T) {
	if merge.HasConflictMarkers("plain text\nwith no markers\n") {
		t.Error("clean text reported as conflicted")
	}
	if !merge.HasConflictMarkers("a\n<<<<<<< codex\nb\n=======\nc\n>>>>>>> windsurf\n") {
		t.Error("marked text reported as clean")
	}
}

// A file whose last line has no newline used to share no line with a side that
// added one, turning two edits at opposite ends into a bogus overlap.
func TestThreeWay_MissingFinalNewlineIsNotAnOverlap(t *testing.T) {
	got, conflicted := merge.ThreeWay("say hello", []merge.Side{
		{Label: "claude-code", Text: "from claude\nsay hello\n"},
		{Label: "codex", Text: "say hello\nfrom codex\n"},
	})
	if conflicted {
		t.Fatalf("edits either side of one line must merge cleanly, got:\n%s", got)
	}
	want := "from claude\nsay hello\nfrom codex\n"
	if got != want {
		t.Errorf("merged = %q, want %q", got, want)
	}
}

// Two changes with no unchanged base line between them are one conflict, even
// though their line ranges do not intersect. That is diff3's rule and what
// `git merge-file` produces for the same three inputs; it is pinned here because
// "they are on different lines, why is this a conflict" is a reasonable thing to
// wonder later.
func TestThreeWay_AbuttingChangesConflictLikeGitMergeFile(t *testing.T) {
	got, conflicted := merge.ThreeWay("one\ntwo\nthree\n", []merge.Side{
		{Label: "codex", Text: "ONE\ntwo\nthree\n"},
		{Label: "windsurf", Text: "one\nTWO\nthree\n"},
	})
	if !conflicted {
		t.Fatalf("abutting rewrites must be reported, got:\n%s", got)
	}
	if !strings.HasSuffix(got, "three\n") {
		t.Errorf("the untouched tail was swallowed by the conflict region:\n%s", got)
	}
}

// Two insertions at the same point have empty base ranges, so they never
// intersect. They still compete for one position, and nothing in the text says
// which goes first.
func TestThreeWay_InsertionsAtTheSamePointConflict(t *testing.T) {
	got, conflicted := merge.ThreeWay("one\ntwo\n", []merge.Side{
		{Label: "codex", Text: "one\nFROM CODEX\ntwo\n"},
		{Label: "windsurf", Text: "one\nFROM WINDSURF\ntwo\n"},
	})
	if !conflicted {
		t.Fatalf("competing insertions must not be silently concatenated, got:\n%s", got)
	}
	for _, want := range []string{"<<<<<<< codex", ">>>>>>> windsurf"} {
		if !strings.Contains(got, want) {
			t.Errorf("merged text missing %q:\n%s", want, got)
		}
	}
}
