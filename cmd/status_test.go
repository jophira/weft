package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/instruction"
	"github.com/jophira/weft/internal/manifest"
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
	renderStatus(&buf, "hybrid", []harnessStatus{
		{Harness: "a", Drift: "ok"},
		{Harness: "b", Drift: "drift"},
		{Harness: "c", Drift: "n/a"},
	}, true)
	got := buf.String()
	if !strings.Contains(got, "weft: hybrid") || !strings.Contains(got, "3 harness") || !strings.Contains(got, "drift:1") {
		t.Errorf("short status = %q", got)
	}
}

func TestRenderStatus_emptyMentionsNoHarnesses(t *testing.T) {
	var buf bytes.Buffer
	renderStatus(&buf, "", nil, false)
	got := buf.String()
	if !strings.Contains(got, "Active profile: none") || !strings.Contains(got, "No harnesses applied") {
		t.Errorf("empty status = %q", got)
	}
}
