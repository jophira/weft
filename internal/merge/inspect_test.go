package merge_test

import (
	"testing"

	"github.com/jophira/weft/internal/merge"
	"github.com/jophira/weft/internal/profile"
)

// ── Single source ─────────────────────────────────────────────────────────────

func TestInspect_singleSource_noConflicts(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "CLAUDE.md", "rules")
	writeFile(t, src, "commands/hello.md", "cmd")

	report, err := merge.New(profile.OverlayCascade).Inspect([]string{src})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(report.Conflicts()) != 0 {
		t.Errorf("conflicts = %d, want 0", len(report.Conflicts()))
	}
	if len(report.Unique()) != 2 {
		t.Errorf("unique = %d, want 2", len(report.Unique()))
	}
	if report.Unique()[0].WinnerRoot != src {
		t.Errorf("winner = %q, want src root", report.Unique()[0].WinnerRoot)
	}
}

// ── Cascade: last root wins ───────────────────────────────────────────────────

func TestInspect_cascade_lastRootWins(t *testing.T) {
	base := t.TempDir()
	overlay := t.TempDir()
	writeFile(t, base, "CLAUDE.md", "base")
	writeFile(t, overlay, "CLAUDE.md", "overlay")

	report, err := merge.New(profile.OverlayCascade).Inspect([]string{base, overlay})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	conflicts := report.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(conflicts))
	}
	if conflicts[0].WinnerRoot != overlay {
		t.Errorf("winner = %q, want overlay root", conflicts[0].WinnerRoot)
	}
	if len(conflicts[0].Roots) != 2 {
		t.Errorf("roots len = %d, want 2", len(conflicts[0].Roots))
	}
}

// ── LastWins: last root wins ──────────────────────────────────────────────────

func TestInspect_lastWins_lastRootWins(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	c := t.TempDir()
	writeFile(t, a, "CLAUDE.md", "a")
	writeFile(t, b, "CLAUDE.md", "b")
	writeFile(t, c, "CLAUDE.md", "c")

	report, err := merge.New(profile.OverlayLastWins).Inspect([]string{a, b, c})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	conflicts := report.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(conflicts))
	}
	if conflicts[0].WinnerRoot != c {
		t.Errorf("winner = %q, want c (last root)", conflicts[0].WinnerRoot)
	}
}

// ── Merge: no single winner ───────────────────────────────────────────────────

func TestInspect_merge_noWinner(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	writeFile(t, a, "CLAUDE.md", "a")
	writeFile(t, b, "CLAUDE.md", "b")

	report, err := merge.New(profile.OverlayMerge).Inspect([]string{a, b})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	conflicts := report.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(conflicts))
	}
	if conflicts[0].WinnerRoot != "" {
		t.Errorf("winner = %q, want empty (merge combines all)", conflicts[0].WinnerRoot)
	}
}

// ── Unique files classified correctly ────────────────────────────────────────

func TestInspect_uniqueFiles(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	writeFile(t, a, "commands/alpha.md", "alpha")
	writeFile(t, b, "commands/beta.md", "beta")

	report, err := merge.New(profile.OverlayCascade).Inspect([]string{a, b})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(report.Conflicts()) != 0 {
		t.Errorf("conflicts = %d, want 0", len(report.Conflicts()))
	}
	if len(report.Unique()) != 2 {
		t.Errorf("unique = %d, want 2", len(report.Unique()))
	}
}

// ── Filter is respected ───────────────────────────────────────────────────────

func TestInspect_filterRespected(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "CLAUDE.md", "rules")
	writeFile(t, src, "cache/state.json", "internal")

	filter := func(rel string) bool { return rel == "CLAUDE.md" }
	report, err := merge.New(profile.OverlayCascade).WithFilter(filter).Inspect([]string{src})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(report.Entries) != 1 {
		t.Errorf("entries = %d, want 1 (filter must exclude cache/)", len(report.Entries))
	}
	if report.Entries[0].Rel != "CLAUDE.md" {
		t.Errorf("entry = %q, want CLAUDE.md", report.Entries[0].Rel)
	}
}

// ── Entries are sorted ────────────────────────────────────────────────────────

func TestInspect_entriesSorted(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "zzz.md", "z")
	writeFile(t, src, "aaa.md", "a")
	writeFile(t, src, "mmm.md", "m")

	report, err := merge.New(profile.OverlayCascade).Inspect([]string{src})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	for i := 1; i < len(report.Entries); i++ {
		if report.Entries[i-1].Rel > report.Entries[i].Rel {
			t.Errorf("entries not sorted: %v > %v", report.Entries[i-1].Rel, report.Entries[i].Rel)
		}
	}
}

// ── Roots list in Roots field preserves source order ─────────────────────────

func TestInspect_rootsPreserveOrder(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	c := t.TempDir()
	writeFile(t, a, "CLAUDE.md", "a")
	writeFile(t, b, "CLAUDE.md", "b")
	writeFile(t, c, "CLAUDE.md", "c")

	report, err := merge.New(profile.OverlayCascade).Inspect([]string{a, b, c})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	conflicts := report.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(conflicts))
	}
	e := conflicts[0]
	if e.Roots[0] != a || e.Roots[1] != b || e.Roots[2] != c {
		t.Errorf("roots order wrong: %v", e.Roots)
	}
}
