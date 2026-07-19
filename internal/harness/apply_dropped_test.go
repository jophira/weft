package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/manifest"
)

// applyFixture wires a GenericHarness to a fixed target dir so tests can drive
// applyWithManifest directly, re-using one ApplyCtx (and therefore one manifest)
// across successive applies the way a real profile switch does.
type applyFixture struct {
	target string
	ctx    ApplyCtx
	buf    *bytes.Buffer
}

func newApplyFixture(t *testing.T) *applyFixture {
	t.Helper()
	buf := &bytes.Buffer{}
	return &applyFixture{
		target: t.TempDir(),
		ctx:    ApplyCtx{ProfileName: "test", CfgDir: t.TempDir(), Out: buf},
		buf:    buf,
	}
}

// apply projects files (rel path -> content) as one profile, returning the log.
func (f *applyFixture) apply(t *testing.T, files map[string]string) string {
	t.Helper()
	staged := t.TempDir()
	for rel, content := range files {
		write(t, filepath.Join(staged, rel), content)
	}
	f.buf.Reset()
	h := &GenericHarness{name: "test-harness", root: f.target}
	if err := h.Apply(staged, f.ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return f.buf.String()
}

func (f *applyFixture) manifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Load(f.ctx.CfgDir, "test-harness")
	if err != nil {
		t.Fatalf("loading manifest: %v", err)
	}
	return m
}

func (f *applyFixture) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.target, rel))
	return err == nil
}

// osPath converts a slash-separated test path to the separator the apply log and
// manifest keys actually use, which is the OS one (filepath.Rel on Windows yields
// backslashes). Lets the assertions below stay readable as "a/b/c".
func osPath(rel string) string { return filepath.FromSlash(rel) }

// TestApply_ProfileRoundTripKeepsOwnership is the issue #209 regression: a file
// that leaves the staged set and comes back must still be recognised as weft's
// own output, not mistaken for a user edit and backed up.
func TestApply_ProfileRoundTripKeepsOwnership(t *testing.T) {
	f := newApplyFixture(t)
	profileA := map[string]string{
		"CLAUDE.md":              "shared",
		"skills/foo/SKILL.md":    "foo v1",
		"skills/foo/config.yaml": "key: value",
	}
	profileB := map[string]string{"CLAUDE.md": "shared"}

	f.apply(t, profileA)
	f.apply(t, profileB) // drops skills/foo entirely
	out := f.apply(t, profileA)

	if strings.Contains(out, "externally modified") {
		t.Errorf("re-applying profile A must not report a conflict, got: %q", out)
	}
	if !strings.Contains(out, "✓ wrote     "+osPath("skills/foo/SKILL.md")) {
		t.Errorf("expected skills/foo/SKILL.md to be re-written, got: %q", out)
	}
	// Nothing should have been backed up — the backup dir should not even exist.
	if entries, err := os.ReadDir(filepath.Join(f.ctx.CfgDir, "backups")); err == nil && len(entries) > 0 {
		t.Errorf("no backups expected on a clean round trip, found %d", len(entries))
	}
}

// TestApply_UnchangedAcrossRoundTripWhenFileSurvives covers the subtler half of
// #209: a file present in both profiles must log "unchanged", not be rewritten.
func TestApply_UnchangedAcrossRoundTripWhenFileSurvives(t *testing.T) {
	f := newApplyFixture(t)
	f.apply(t, map[string]string{"CLAUDE.md": "shared", "extra.md": "x"})
	out := f.apply(t, map[string]string{"CLAUDE.md": "shared"})

	if !strings.Contains(out, "· unchanged CLAUDE.md") {
		t.Errorf("expected CLAUDE.md unchanged, got: %q", out)
	}
}

// TestApply_DroppedFileRemoved verifies the decision recorded on #209: a file weft
// owns that leaves the profile is deleted from the target and logged.
func TestApply_DroppedFileRemoved(t *testing.T) {
	f := newApplyFixture(t)
	f.apply(t, map[string]string{"CLAUDE.md": "shared", "commands/gone.md": "bye"})
	out := f.apply(t, map[string]string{"CLAUDE.md": "shared"})

	if f.exists("commands/gone.md") {
		t.Error("dropped file should have been removed from the target")
	}
	if !strings.Contains(out, "− removed   "+osPath("commands/gone.md")) {
		t.Errorf("expected a removal log line, got: %q", out)
	}
	if _, stillTracked := f.manifest(t).Files[osPath("commands/gone.md")]; stillTracked {
		t.Error("removed file should be pruned from the manifest")
	}
}

// TestApply_DroppedFileRemovalPrunesEmptyDirs verifies dropping a whole skill does
// not leave a bare directory behind, while a directory still holding files stays.
func TestApply_DroppedFileRemovalPrunesEmptyDirs(t *testing.T) {
	f := newApplyFixture(t)
	f.apply(t, map[string]string{
		"skills/solo/SKILL.md": "solo",
		"skills/kept/SKILL.md": "kept",
		"skills/kept/extra.md": "extra",
	})
	f.apply(t, map[string]string{"skills/kept/SKILL.md": "kept"})

	if f.exists("skills/solo") {
		t.Error("emptied skills/solo directory should have been pruned")
	}
	if !f.exists("skills/kept") {
		t.Error("skills/kept still holds a staged file and must survive")
	}
	if !f.exists("skills") {
		t.Error("target subtree root must not be pruned while still in use")
	}
}

// TestApply_DroppedFileEditedByUserIsKept verifies weft will not delete a dropped
// file the user has edited — deleting their work is not weft's call.
func TestApply_DroppedFileEditedByUserIsKept(t *testing.T) {
	f := newApplyFixture(t)
	f.apply(t, map[string]string{"CLAUDE.md": "shared", "commands/mine.md": "original"})

	edited := filepath.Join(f.target, "commands", "mine.md")
	write(t, edited, "hand-edited by the user")

	out := f.apply(t, map[string]string{"CLAUDE.md": "shared"})

	if !f.exists("commands/mine.md") {
		t.Fatal("user-edited file must not be deleted when dropped from the profile")
	}
	if got := readFile(t, edited); got != "hand-edited by the user" {
		t.Errorf("user edit must be preserved verbatim, got %q", got)
	}
	if !strings.Contains(out, "! kept      "+osPath("commands/mine.md")) {
		t.Errorf("expected a 'kept' warning, got: %q", out)
	}
}

// TestApply_LegacyManifestWithoutStagedDeletesNothing verifies backward compat:
// manifests written before the Staged field must not trigger a mass deletion on
// the first apply after upgrading.
func TestApply_LegacyManifestWithoutStagedDeletesNothing(t *testing.T) {
	f := newApplyFixture(t)
	files := map[string]string{"CLAUDE.md": "shared", "commands/foo.md": "foo"}
	f.apply(t, files)

	// Simulate a pre-Staged manifest: ownership recorded, staged set absent.
	m := f.manifest(t)
	if len(m.Staged) == 0 {
		t.Fatal("fixture precondition: expected Staged to be populated")
	}
	m.Staged = nil
	if err := manifest.Save(f.ctx.CfgDir, m); err != nil {
		t.Fatal(err)
	}

	out := f.apply(t, files)

	if strings.Contains(out, "− removed") {
		t.Errorf("legacy manifest must not cause deletions, got: %q", out)
	}
	if !f.exists("commands/foo.md") || !f.exists("CLAUDE.md") {
		t.Error("no file should have been deleted on the first post-upgrade apply")
	}
}

// TestApply_StagedTracksCurrentProfileOnly verifies Staged narrows to the active
// profile while Files retains ownership of everything weft has written.
func TestApply_StagedTracksCurrentProfileOnly(t *testing.T) {
	f := newApplyFixture(t)
	f.apply(t, map[string]string{"CLAUDE.md": "shared", "commands/keep.md": "keep"})

	// Edit so the file is kept rather than removed — that is the case where Files
	// and Staged must legitimately diverge.
	write(t, filepath.Join(f.target, "commands", "keep.md"), "user edit")
	f.apply(t, map[string]string{"CLAUDE.md": "shared"})

	m := f.manifest(t)
	if got := m.Staged; len(got) != 1 || got[0] != "CLAUDE.md" {
		t.Errorf("Staged = %v, want [CLAUDE.md]", got)
	}
	if _, owned := m.Files[osPath("commands/keep.md")]; !owned {
		t.Error("Files must retain ownership of the kept file so write-back still works")
	}
}
