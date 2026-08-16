package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/locate"
)

// withHarnessHome points harness path resolution at dir for the duration of a
// test, restoring it afterwards.
func withHarnessHome(t *testing.T, dir string) {
	t.Helper()
	locate.SetHarnessHome(dir)
	t.Cleanup(func() { locate.SetHarnessHome("") })
}

// populatedHarnessHome returns a directory holding every built-in harness's
// config root, so detection succeeds under the override.
//
// Creating them is part of the assertion rather than setup noise: a generic
// harness resolves its root through the locate candidates, so it only detects
// here if detection is reading the override too.
func populatedHarnessHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, rel := range [][]string{
		{".claude"}, {".codex"}, {".cursor"}, {".codeium", "windsurf"}, {".gemini"}, {".aider"},
		{".gemini", "antigravity"}, {".config", "opencode"}, {".hermes"}, {".config", "goose"},
	} {
		if err := os.MkdirAll(filepath.Join(append([]string{home}, rel...)...), 0o755); err != nil {
			t.Fatalf("creating %v: %v", rel, err)
		}
	}
	return home
}

// TestInstructionSpecsHonourHarnessHome is the regression guard for #265.
//
// --config isolated only weft's own state, so an apply run under a throwaway
// config still wrote into the real ~/.claude, ~/.codex and the rest. Every
// harness that names an instruction file must resolve it under the override,
// because one adapter still calling os.UserHomeDir would leak a write into the
// user's real setup and the isolation would look complete while not being so.
func TestInstructionSpecsHonourHarnessHome(t *testing.T) {
	fake := populatedHarnessHome(t)
	withHarnessHome(t, fake)

	var checked int
	for _, k := range builtins() {
		ic, ok := k.H.(InstructionConsumer)
		if !ok {
			continue // no root instruction file (Warp)
		}
		spec, err := ic.InstructionSpec()
		if err != nil {
			t.Errorf("%s: InstructionSpec: %v", k.H.Name(), err)
			continue
		}
		checked++
		if !strings.HasPrefix(spec.Path, fake+string(filepath.Separator)) {
			t.Errorf("%s: instruction path %q is outside the harness home %q",
				k.H.Name(), spec.Path, fake)
		}
	}
	if checked == 0 {
		t.Fatal("no harness reported an instruction file; the check proved nothing")
	}
}

// A generic harness resolves its config root through locate candidates rather
// than homeJoin, so it needs its own assertion.
func TestGenericHarnessConfigPathHonoursHarnessHome(t *testing.T) {
	fake := populatedHarnessHome(t)
	withHarnessHome(t, fake)

	for _, k := range builtins() {
		cp, ok := k.H.(ConfigPather)
		if !ok {
			continue
		}
		path := cp.ConfigPath()
		if path == "" {
			continue // nothing resolved on this platform
		}
		if !strings.HasPrefix(path, fake+string(filepath.Separator)) {
			t.Errorf("%s: config path %q is outside the harness home %q", k.H.Name(), path, fake)
		}
	}
}

func TestHarnessHomeRestoresRealHomeWhenCleared(t *testing.T) {
	fake := t.TempDir()
	locate.SetHarnessHome(fake)
	if got := locate.HarnessHome(); got != fake {
		t.Fatalf("HarnessHome() = %q, want the override %q", got, fake)
	}
	locate.SetHarnessHome("")
	if got := locate.HarnessHome(); got == fake || got == "" {
		t.Errorf("HarnessHome() = %q after clearing, want the real home", got)
	}
}
