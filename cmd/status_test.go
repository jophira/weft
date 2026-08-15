package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jophira/weft/internal/instruction"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/runstate"
)

// saveManifestForStatus writes a manifest carrying a managed instruction block
// recorded at the given path with the given body hash.
func saveManifestForStatus(t *testing.T, cfgDir, harness, profile, instrPath, blockHash string) {
	t.Helper()
	m := &manifest.Manifest{
		Harness:          harness,
		Profile:          profile,
		Files:            map[string]string{},
		InstructionPath:  instrPath,
		InstructionBlock: blockHash,
	}
	if err := manifest.Save(cfgDir, m); err != nil {
		t.Fatal(err)
	}
}

func TestCollectHarnessStatus_okAndDrift(t *testing.T) {
	cfgDir := t.TempDir()

	// Harness "ok": on-disk block matches the recorded hash.
	okPath := filepath.Join(t.TempDir(), "AGENTS.md")
	body := instruction.InlineBody([]instruction.SourceContent{{Name: "s", Content: "rule"}})
	writeFile(t, okPath, string(instruction.Upsert(nil, body)))
	saveManifestForStatus(t, cfgDir, "okharness", "prof", okPath, manifest.HashBytes([]byte(body)))

	// Harness "drift": on-disk block differs from the recorded hash.
	driftPath := filepath.Join(t.TempDir(), "AGENTS.md")
	writeFile(t, driftPath, string(instruction.Upsert(nil, "EDITED BODY")))
	saveManifestForStatus(t, cfgDir, "driftharness", "prof", driftPath, manifest.HashBytes([]byte(body)))

	statuses, err := collectHarnessStatus(cfgDir)
	if err != nil {
		t.Fatalf("collectHarnessStatus: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("got %d statuses, want 2", len(statuses))
	}
	// Sorted by harness name: driftharness, okharness.
	if statuses[0].Harness != "driftharness" || statuses[0].Drift != "drift" {
		t.Errorf("status[0] = %+v, want driftharness/drift", statuses[0])
	}
	if statuses[1].Harness != "okharness" || statuses[1].Drift != "ok" {
		t.Errorf("status[1] = %+v, want okharness/ok", statuses[1])
	}
}

func TestInstructionDrift_missingFile(t *testing.T) {
	m := &manifest.Manifest{
		InstructionPath:  filepath.Join(t.TempDir(), "gone.md"),
		InstructionBlock: "sha256:deadbeef",
	}
	if got := instructionDrift(m); got != "missing" {
		t.Errorf("instructionDrift = %q, want missing", got)
	}
}

func TestInstructionDrift_noBlockIsNA(t *testing.T) {
	if got := instructionDrift(&manifest.Manifest{}); got != "n/a" {
		t.Errorf("instructionDrift = %q, want n/a", got)
	}
}

func TestRenderStatus_short(t *testing.T) {
	var buf bytes.Buffer
	renderStatus(&buf, "hybrid", nil, nil, []harnessStatus{
		{Harness: "a", Drift: "ok"},
		{Harness: "b", Drift: "drift"},
		{Harness: "c", Drift: "n/a"},
	}, true)
	got := buf.String()
	if !strings.Contains(got, "weft: hybrid") || !strings.Contains(got, "3 harness") || !strings.Contains(got, "drift:1") {
		t.Errorf("short status = %q", got)
	}
	if !strings.Contains(got, "watch:off") {
		t.Errorf("short status missing watch state: %q", got)
	}
}

// TestRenderStatus_shortWithCounts covers the status line affordance: a cached
// tally reaches --short without the render walking any harness root.
func TestRenderStatus_shortWithCounts(t *testing.T) {
	var buf bytes.Buffer
	counts := &runstate.Counts{Adoptable: 18, Conflicts: 2, UpdatedAt: time.Now()}
	renderStatus(&buf, "hybrid", nil, counts, []harnessStatus{{Harness: "a", Drift: "ok"}}, true)
	got := buf.String()
	if !strings.Contains(got, "adopt:18") || !strings.Contains(got, "conflict:2") {
		t.Errorf("short status = %q, want it to carry both counts", got)
	}
}

// TestRenderStatus_shortOmitsAbsentCounts pins the difference between "no scan
// has run" and "a scan found nothing". Printing adopt:0 for the first asserts a
// scan happened, which is a claim the render cannot make.
func TestRenderStatus_shortOmitsAbsentCounts(t *testing.T) {
	var buf bytes.Buffer
	renderStatus(&buf, "hybrid", nil, nil, []harnessStatus{{Harness: "a", Drift: "ok"}}, true)
	if got := buf.String(); strings.Contains(got, "adopt:") || strings.Contains(got, "conflict:") {
		t.Errorf("short status = %q, want no counts when none are cached", got)
	}
}

// TestRenderStatus_shortOmitsStaleCounts covers the other silence case: a number
// from days ago sends the user looking for files that have since moved.
func TestRenderStatus_shortOmitsStaleCounts(t *testing.T) {
	var buf bytes.Buffer
	old := &runstate.Counts{Adoptable: 18, Conflicts: 2, UpdatedAt: time.Now().Add(-72 * time.Hour)}
	renderStatus(&buf, "hybrid", nil, old, []harnessStatus{{Harness: "a", Drift: "ok"}}, true)
	if got := buf.String(); strings.Contains(got, "adopt:") {
		t.Errorf("short status = %q, want stale counts dropped", got)
	}
}

// TestRenderStatus_longNamesTheCountAge. The full report has room to say when the
// numbers were taken, which --short does not, so it says it.
func TestRenderStatus_longNamesTheCountAge(t *testing.T) {
	var buf bytes.Buffer
	at := time.Date(2026, 8, 15, 14, 2, 0, 0, time.UTC)
	counts := &runstate.Counts{Adoptable: 3, Conflicts: 1, UpdatedAt: at}
	renderStatus(&buf, "hybrid", nil, counts, nil, false)
	got := buf.String()
	if !strings.Contains(got, "Adoptable: 3") || !strings.Contains(got, "conflicts: 1") {
		t.Errorf("long status = %q, want both counts", got)
	}
	if !strings.Contains(got, "2026-08-15 14:02") {
		t.Errorf("long status = %q, want it to date the counts", got)
	}
}

func TestRenderStatus_emptyMentionsNoHarnesses(t *testing.T) {
	var buf bytes.Buffer
	renderStatus(&buf, "", nil, nil, nil, false)
	got := buf.String()
	if !strings.Contains(got, "Active profile: none") || !strings.Contains(got, "No harnesses applied") {
		t.Errorf("empty status = %q", got)
	}
	if !strings.Contains(got, "Watcher: not running") {
		t.Errorf("empty status missing watcher line: %q", got)
	}
}

func TestRenderStatus_watcherRunning(t *testing.T) {
	var buf bytes.Buffer
	rs := &runstate.RunState{PID: 4242, Profile: "hybrid", StartedAt: time.Now().Add(-90 * time.Minute)}
	renderStatus(&buf, "hybrid", rs, nil, nil, false)
	got := buf.String()
	if !strings.Contains(got, "Watcher: running") || !strings.Contains(got, "pid 4242") || !strings.Contains(got, `profile "hybrid"`) {
		t.Errorf("running watcher status = %q", got)
	}
}
