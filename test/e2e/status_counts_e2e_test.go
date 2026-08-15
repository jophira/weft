//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusCountsE2E drives the status-line counts through the real binary
// against an isolated $HOME.
//
// The point of the cache is that `weft status --short` renders from a file
// rather than a filesystem sweep, so the test asserts the two ends of that: a
// fresh home prints no counts because no apply has recorded any, and an apply
// leaves a cache the next status read picks up. A file dropped into a harness
// that no source owns moves the adoptable count, which is what proves the
// number came from a real scan rather than a zero-valued struct.
func TestStatusCountsE2E(t *testing.T) {
	home := t.TempDir()
	mustMkdirAll(t, filepath.Join(home, ".claude"))

	src := filepath.Join(t.TempDir(), "personal")
	writeFile(t, filepath.Join(src, "CLAUDE.md"), "# personal rules\n")
	runWeft(t, home, "source", "add", "personal", src)
	runWeft(t, home, "profile", "create", "solo", "--sources", "personal", "--target", "claude-code")

	// ── Before any apply: silence, not zero ───────────────────────────────────
	before := runWeft(t, home, "status", "--short")
	if strings.Contains(before, "adopt:") || strings.Contains(before, "conflict:") {
		t.Errorf("status --short on a fresh home = %q, want no counts before an apply", before)
	}

	// ── An apply records the cache ────────────────────────────────────────────
	runWeft(t, home, "profile", "use", "solo", "--no-watch")

	applied := runWeft(t, home, "status", "--short")
	mustContain(t, "status after apply", applied, "adopt:")
	mustContain(t, "status after apply", applied, "conflict:0")

	countsPath := filepath.Join(home, ".config", "weft", "counts.json")
	cache := readFile(t, countsPath)
	mustContain(t, "counts cache names the profile", cache, `"profile": "solo"`)

	// ── An unowned file raises the adoptable count ────────────────────────────
	// Written straight into the harness, never through a source, which is exactly
	// what `weft adopt --scan` exists to find.
	writeFile(t, filepath.Join(home, ".claude", "commands", "handwritten.md"), "# authored in the harness\n")
	runWeft(t, home, "profile", "use", "solo", "--no-watch")

	after := runWeft(t, home, "status", "--short")
	if strings.Contains(after, "adopt:0") {
		t.Errorf("status --short = %q, want the unowned command counted", after)
	}

	// The long form dates the numbers; --short has no room to.
	long := runWeft(t, home, "status")
	mustContain(t, "long status", long, "Adoptable:")
	mustContain(t, "long status", long, "conflicts:")
}
