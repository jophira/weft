//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTargetDetectE2E drives 'weft target detect' through the real binary against
// an isolated $HOME holding two harness config roots: one the profile already
// targets through the legacy active_target key, one it does not.
//
// It asserts the two halves of the contract that matter on disk: the plain form
// writes nothing, and --add migrates active_target to targets while keeping the
// existing value first.
func TestTargetDetectE2E(t *testing.T) {
	home := t.TempDir()

	// Both harnesses detect on their config root, so the result does not depend
	// on what the developer happens to have on PATH.
	mustMkdirAll(t, filepath.Join(home, ".claude"))
	mustMkdirAll(t, filepath.Join(home, ".codex"))

	src := filepath.Join(t.TempDir(), "personal")
	writeFile(t, filepath.Join(src, "CLAUDE.md"), "# personal rules\n")
	runWeft(t, home, "source", "add", "personal", src)
	runWeft(t, home, "profile", "create", "legacy", "--sources", "personal", "--target", "claude-code")

	// Rewrite the profile into the pre-targets format this migration exists for.
	profilePath := filepath.Join(home, ".config", "weft", "profiles", "legacy.yaml")
	yaml := readFile(t, profilePath)
	legacy := strings.Replace(yaml, "targets:\n    - claude-code\n", "active_target: claude-code\n", 1)
	if legacy == yaml {
		t.Fatalf("test setup: profile YAML did not carry the expected targets list:\n%s", yaml)
	}
	writeFile(t, profilePath, legacy)
	runWeft(t, home, "profile", "use", "legacy", "--no-watch")

	// ── Plain form: report only ───────────────────────────────────────────────
	report := runWeft(t, home, "target", "detect")
	mustContain(t, "detect report", report, "claude-code")
	mustContain(t, "detect report names the signal", report, "config ~/.claude")
	mustContain(t, "detect report suggests --add", report, "weft target detect --add")
	if got := readFile(t, profilePath); got != legacy {
		t.Errorf("profile changed without --add:\n%s", got)
	}

	// ── --add: persist, migrating active_target ───────────────────────────────
	added := runWeft(t, home, "target", "detect", "--add")
	mustContain(t, "add output", added, "+ codex")

	updated := readFile(t, profilePath)
	mustNotContain(t, "migrated profile", updated, "active_target")
	mustOrder(t, "targets keep the existing value first", updated, "targets:", "claude-code", "codex")

	// ── Applying to the widened list writes a manifest per harness ────────────
	runWeft(t, home, "profile", "use", "legacy", "--no-watch")
	for _, name := range []string{"claude-code", "codex"} {
		manifestPath := filepath.Join(home, ".config", "weft", "manifests", name+".json")
		if _, err := os.Stat(manifestPath); err != nil {
			t.Errorf("expected a manifest for %s: %v", name, err)
		}
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
