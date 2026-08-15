package profile_test

import (
	"slices"
	"testing"

	"github.com/jophira/weft/internal/profile"
)

// found is a detection that matched a config root, the common case.
func found(name string) profile.Detection {
	return profile.Detection{Name: name, Found: true, Signal: "config ~/." + name}
}

func missing(name string) profile.Detection {
	return profile.Detection{Name: name}
}

// rowFor returns the report row for a harness, failing when it has none.
func rowFor(t *testing.T, rows []profile.TargetStatus, name string) profile.TargetStatus {
	t.Helper()
	for _, r := range rows {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no report row for %q (rows: %+v)", name, rows)
	return profile.TargetStatus{}
}

// ── TargetReport ──────────────────────────────────────────────────────────────

func TestTargetReport_reportsSignalAndTargeting(t *testing.T) {
	p := &profile.Profile{Targets: []string{"claude-code"}}
	rows := profile.TargetReport(p, []profile.Detection{found("claude-code"), found("codex"), missing("cursor")})

	claude := rowFor(t, rows, "claude-code")
	if !claude.Targeted || claude.New || claude.Untraced {
		t.Errorf("claude-code = %+v, want targeted and detected", claude)
	}
	if claude.Signal != "config ~/.claude-code" {
		t.Errorf("claude-code signal = %q, want the detection's signal", claude.Signal)
	}
	codex := rowFor(t, rows, "codex")
	if !codex.New || codex.Targeted {
		t.Errorf("codex = %+v, want detected but not targeted", codex)
	}
	cursor := rowFor(t, rows, "cursor")
	if cursor.New || cursor.Targeted || cursor.Untraced {
		t.Errorf("cursor = %+v, want neither detected nor targeted", cursor)
	}
}

func TestTargetReport_configuredButUndetectedIsKept(t *testing.T) {
	p := &profile.Profile{Targets: []string{"claude-code", "codex"}}
	rows := profile.TargetReport(p, []profile.Detection{found("claude-code"), missing("codex")})

	codex := rowFor(t, rows, "codex")
	if !codex.Untraced || !codex.Targeted {
		t.Errorf("codex = %+v, want reported as targeted but no longer detected", codex)
	}
}

func TestTargetReport_unknownConfiguredTargetGetsARow(t *testing.T) {
	p := &profile.Profile{Targets: []string{"claude-code", "homegrown"}}
	rows := profile.TargetReport(p, []profile.Detection{found("claude-code")})

	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (a row for the harness this build does not know)", len(rows))
	}
	homegrown := rowFor(t, rows, "homegrown")
	if !homegrown.Targeted || !homegrown.Untraced {
		t.Errorf("homegrown = %+v, want targeted and undetected", homegrown)
	}
}

func TestTargetReport_legacyActiveTargetCountsAsTargeted(t *testing.T) {
	p := &profile.Profile{ActiveTarget: "claude-code"}
	rows := profile.TargetReport(p, []profile.Detection{found("claude-code")})

	if r := rowFor(t, rows, "claude-code"); !r.Targeted || r.New {
		t.Errorf("claude-code = %+v, want targeted via active_target", r)
	}
}

// ── AddDetected ───────────────────────────────────────────────────────────────

func TestAddDetected_migratesActiveTargetKeepingItFirst(t *testing.T) {
	p := &profile.Profile{ActiveTarget: "claude-code"}
	rows := profile.TargetReport(p, []profile.Detection{found("codex"), found("claude-code")})

	added, changed := profile.AddDetected(p, rows)
	if !changed {
		t.Fatal("changed = false, want the migration to count as a change")
	}
	if !slices.Equal(added, []string{"codex"}) {
		t.Errorf("added = %v, want [codex]", added)
	}
	if !slices.Equal(p.Targets, []string{"claude-code", "codex"}) {
		t.Errorf("Targets = %v, want the existing active_target first", p.Targets)
	}
	if p.ActiveTarget != "" {
		t.Errorf("ActiveTarget = %q, want it cleared by the migration", p.ActiveTarget)
	}
}

func TestAddDetected_migratesEvenWithNothingNew(t *testing.T) {
	p := &profile.Profile{ActiveTarget: "claude-code"}
	rows := profile.TargetReport(p, []profile.Detection{found("claude-code")})

	added, changed := profile.AddDetected(p, rows)
	if !changed || len(added) != 0 {
		t.Fatalf("added = %v, changed = %v, want no additions but a migration", added, changed)
	}
	if !slices.Equal(p.Targets, []string{"claude-code"}) || p.ActiveTarget != "" {
		t.Errorf("Targets = %v, ActiveTarget = %q, want the list form alone", p.Targets, p.ActiveTarget)
	}
}

func TestAddDetected_alreadyListedIsNotDuplicated(t *testing.T) {
	p := &profile.Profile{Targets: []string{"claude-code", "codex"}}
	rows := profile.TargetReport(p, []profile.Detection{found("claude-code"), found("codex")})

	added, changed := profile.AddDetected(p, rows)
	if changed || len(added) != 0 {
		t.Fatalf("added = %v, changed = %v, want no change", added, changed)
	}
	if !slices.Equal(p.Targets, []string{"claude-code", "codex"}) {
		t.Errorf("Targets = %v, want it untouched", p.Targets)
	}
}

func TestAddDetected_neverRemovesAnUndetectedTarget(t *testing.T) {
	p := &profile.Profile{Targets: []string{"codex"}}
	rows := profile.TargetReport(p, []profile.Detection{found("claude-code"), missing("codex")})

	added, changed := profile.AddDetected(p, rows)
	if !changed || !slices.Equal(added, []string{"claude-code"}) {
		t.Fatalf("added = %v, changed = %v, want [claude-code] added", added, changed)
	}
	if !slices.Equal(p.Targets, []string{"codex", "claude-code"}) {
		t.Errorf("Targets = %v, want the undetected codex kept", p.Targets)
	}
}

func TestAddDetected_reportAloneWritesNothing(t *testing.T) {
	p := &profile.Profile{Targets: []string{"codex"}}
	rows := profile.TargetReport(p, []profile.Detection{found("claude-code")})

	// The report is what the plain command prints; only AddDetected mutates.
	if !slices.Equal(p.Targets, []string{"codex"}) {
		t.Errorf("Targets = %v, want the report to leave the profile alone", p.Targets)
	}
	if n := len(untargeted(rows)); n != 1 {
		t.Errorf("untargeted = %d, want 1 pending addition to report", n)
	}
}

// untargeted lists the rows --add would append, mirroring what the CLI prints.
func untargeted(rows []profile.TargetStatus) []string {
	var names []string
	for _, r := range rows {
		if r.New {
			names = append(names, r.Name)
		}
	}
	return names
}
