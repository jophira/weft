package harness

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeHarness declares a layout without touching the real adapters, so the
// audit's behaviour is tested rather than any one harness's declarations.
type fakeHarness struct {
	known []KnownFile
	state []string
}

func (f *fakeHarness) Name() string                 { return "fake" }
func (f *fakeHarness) Detect() bool                 { return true }
func (f *fakeHarness) Apply(string, ApplyCtx) error { return nil }
func (f *fakeHarness) KnownFiles() []KnownFile      { return f.known }
func (f *fakeHarness) StateEntries() []string       { return f.state }

// bareHarness declares nothing, standing in for a tool weft knows no layout for.
type bareHarness struct{}

func (b *bareHarness) Name() string                 { return "bare" }
func (b *bareHarness) Detect() bool                 { return true }
func (b *bareHarness) Apply(string, ApplyCtx) error { return nil }

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func relNames(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Rel
	}
	return out
}

func has(entries []Entry, rel string) bool {
	for _, e := range entries {
		if e.Rel == rel {
			return true
		}
	}
	return false
}

func TestAudit_absentRootReportsNotExists(t *testing.T) {
	cov := Audit(&fakeHarness{}, filepath.Join(t.TempDir(), "nope"), nil)
	if cov.Exists {
		t.Error("Exists is true for a missing config root")
	}
}

func TestAudit_splitsManagedFromUnmanaged(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "CLAUDE.md"))
	touch(t, filepath.Join(root, "settings.json"))

	h := &fakeHarness{known: []KnownFile{
		{Rel: "CLAUDE.md", Kind: FileInstructions},
		{Rel: "settings.json", Kind: FileSettings},
	}}
	cov := Audit(h, root, map[string]bool{"CLAUDE.md": true})

	if !has(cov.Managed, "CLAUDE.md") {
		t.Errorf("Managed = %v, want CLAUDE.md", relNames(cov.Managed))
	}
	// The actionable half: a file weft recognises and does not write.
	if !has(cov.Unmanaged, "settings.json") {
		t.Errorf("Unmanaged = %v, want settings.json", relNames(cov.Unmanaged))
	}
}

func TestAudit_declaredButAbsentIsNotReported(t *testing.T) {
	root := t.TempDir()
	h := &fakeHarness{known: []KnownFile{{Rel: "keybindings.json", Kind: FileKeybindings}}}
	cov := Audit(h, root, nil)

	if len(cov.Managed)+len(cov.Unmanaged) != 0 {
		t.Errorf("reported %v / %v for a file that is not installed",
			relNames(cov.Managed), relNames(cov.Unmanaged))
	}
	if !cov.Declared {
		t.Error("Declared is false although the harness declared a layout")
	}
}

// A directory counts as managed when weft owns anything inside it, since that is
// what "weft projects this class" means in practice.
func TestAudit_directoryIsManagedWhenWeftOwnsAFileInIt(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "commands", "review.md"))
	touch(t, filepath.Join(root, "commands", "mine.md"))

	h := &fakeHarness{known: []KnownFile{{Rel: "commands", Dir: true, Kind: FileCommands}}}
	cov := Audit(h, root, map[string]bool{"commands/review.md": true})

	if !has(cov.Managed, "commands/") {
		t.Fatalf("Managed = %v, want commands/", relNames(cov.Managed))
	}
	for _, e := range cov.Managed {
		if e.Rel == "commands/" && e.Count != 2 {
			t.Errorf("commands/ Count = %d, want 2 (both files, not just weft's)", e.Count)
		}
	}
}

func TestAudit_directoryWithNoWeftFilesIsUnmanaged(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "plugins", "thing.js"))

	h := &fakeHarness{known: []KnownFile{{Rel: "plugins", Dir: true, Kind: FilePlugins}}}
	cov := Audit(h, root, nil)

	if !has(cov.Unmanaged, "plugins/") {
		t.Errorf("Unmanaged = %v, want plugins/", relNames(cov.Unmanaged))
	}
}

func TestAudit_stateEntriesAreExcludedFromOther(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "sessions", "a.jsonl"))
	touch(t, filepath.Join(root, "surprise.txt"))

	h := &fakeHarness{state: []string{"sessions"}}
	cov := Audit(h, root, nil)

	if cov.Other != 1 || cov.OtherNames[0] != "surprise.txt" {
		t.Errorf("Other = %d %v, want just surprise.txt", cov.Other, cov.OtherNames)
	}
}

// The noise is generational (settings.json.bak-20260801, goals_1.sqlite-wal), so
// naming each one would leave the declaration permanently out of date.
func TestAudit_stateEntriesMatchGlobs(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "settings.json.bak"))
	touch(t, filepath.Join(root, "settings.json.bak-20260801-214856"))
	touch(t, filepath.Join(root, "goals_1.sqlite-wal"))
	touch(t, filepath.Join(root, "real.md"))

	h := &fakeHarness{state: []string{"*.bak", "*.bak-*", "*.sqlite-wal"}}
	cov := Audit(h, root, nil)

	if cov.Other != 1 || cov.OtherNames[0] != "real.md" {
		t.Errorf("Other = %d %v, want just real.md", cov.Other, cov.OtherNames)
	}
}

// A coverage report gets pasted into issues. Naming a credentials file invites
// someone to go and look at it, and weft has no reason to care that it exists.
func TestAudit_sensitiveEntriesAreNeverNamed(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, ".credentials.json"))
	touch(t, filepath.Join(root, "oauth_creds.json"))
	touch(t, filepath.Join(root, "ordinary.txt"))

	cov := Audit(&fakeHarness{}, root, nil)

	for _, n := range cov.OtherNames {
		if IsSensitive(n) {
			t.Errorf("OtherNames includes the sensitive entry %q", n)
		}
	}
	if cov.Other != 1 {
		t.Errorf("Other = %d %v, want only the ordinary file", cov.Other, cov.OtherNames)
	}
}

// Otherwise the report would understate what weft owns.
func TestAudit_managedPathsOutsideAnyDeclarationStillCount(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "odd", "thing.md"))

	cov := Audit(&fakeHarness{}, root, map[string]bool{"odd/thing.md": true})

	if !has(cov.Managed, "odd/thing.md") {
		t.Errorf("Managed = %v, want the undeclared but manifest-owned path", relNames(cov.Managed))
	}
	// And it must not then be double-counted as unrecognised.
	for _, n := range cov.OtherNames {
		if n == "odd" {
			t.Error("a managed path was also counted as unrecognised")
		}
	}
}

// "Weft does not know this layout" and "weft knows it and none of it is here"
// are different answers, and collapsing them hides a gap in weft.
func TestAudit_undeclaredLayoutIsDistinctFromEmpty(t *testing.T) {
	root := t.TempDir()
	if cov := Audit(&bareHarness{}, root, nil); cov.Declared {
		t.Error("Declared is true for a harness that declared nothing")
	}
	h := &fakeHarness{known: []KnownFile{{Rel: "GEMINI.md", Kind: FileInstructions}}}
	if cov := Audit(h, root, nil); !cov.Declared {
		t.Error("Declared is false for a harness that declared a layout")
	}
}

// undeclaredLayouts names the built-ins whose config layout weft has not
// documented, so the gap is visible in code rather than implied by silence.
//
// Each is here because its layout could not be verified against a real
// installation. Inventing paths would be worse than admitting ignorance: the
// report would confidently list files that do not exist and omit ones that do,
// and a reader has no way to tell a guess from a fact.
var undeclaredLayouts = map[string]string{
	"warp":   "workflow YAML only, and the config root varies by platform and Warp version",
	"hermes": "layout not verified against an installation",
	"goose":  "layout not verified against an installation",
}

// Every built-in should describe its own layout, or be listed above with a
// reason. A new adapter added without either fails here.
func TestBuiltinsDeclareTheirLayoutOrSayWhyNot(t *testing.T) {
	for _, k := range builtins() {
		name := k.H.Name()
		declared := len(KnownFilesOf(k.H)) > 0
		_, excused := undeclaredLayouts[name]

		switch {
		case declared && excused:
			t.Errorf("%s declares a layout but is still listed as undeclared; remove it from undeclaredLayouts", name)
		case !declared && !excused:
			t.Errorf("%s declares no known files and gives no reason; declare its layout or add it to undeclaredLayouts", name)
		}
	}
}
