package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise the --record audit path across multiple sources under an
// isolated --config: the per-repo log/snapshot in <repo>/.weft, the deduped
// append semantics, and — critically — that the global rollup lands inside the
// isolated audit dir, never the developer's real ~/.config/weft.

// resolveWithRecord runs `weft rules resolve <repo> --record` with the cache
// disabled (so edits are picked up immediately) and the work-plane KB off.
func resolveWithRecord(t *testing.T, repo string) string {
	t.Helper()
	savedRoot, savedNoCache, savedNoWork := rulesRoot, rulesNoCache, rulesNoWork
	savedRecord, savedManifest, savedRebuild := rulesRecord, rulesShowManife, rulesRebuild
	t.Cleanup(func() {
		rulesRoot, rulesNoCache, rulesNoWork = savedRoot, savedNoCache, savedNoWork
		rulesRecord, rulesShowManife, rulesRebuild = savedRecord, savedManifest, savedRebuild
	})
	rulesRoot, rulesNoCache, rulesNoWork = "", true, true
	rulesRecord, rulesShowManife, rulesRebuild = true, false, false
	return runCmd(t, rulesResolveCmd, []string{repo})
}

// countJSONLines returns the number of non-empty lines in a JSONL file (0 when
// absent) — the audit log's record count.
func countJSONLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// globalRollups lists the *.jsonl rollups in the (isolated) audit dir.
func globalRollups(t *testing.T) []string {
	t.Helper()
	dir := auditDir()
	if dir == "" {
		t.Fatal("auditDir() empty under isolated config")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatalf("glob rollups: %v", err)
	}
	return matches
}

// TestResolveRecord_WritesRepoAndIsolatedGlobal proves a recorded multi-source
// resolve writes the per-repo snapshot + log with both sources, and the global
// rollup inside the isolated audit dir (not the real home).
func TestResolveRecord_WritesRepoAndIsolatedGlobal(t *testing.T) {
	base := withIsolatedConfig(t)
	twoSourceWorld(t, base)
	createProfile(t, "hybrid", "pers", "work")
	activate(t, "hybrid")
	repo := repoWith(t, "pom.xml")

	resolveWithRecord(t, repo)

	// Per-repo snapshot names both contributing sources and a resolution hash.
	latest, err := os.ReadFile(filepath.Join(repo, ".weft", "resolve.latest.json"))
	if err != nil {
		t.Fatalf("read latest snapshot: %v", err)
	}
	snap := string(latest)
	for _, want := range []string{`"resolution_hash"`, `"pers"`, `"work"`, `"java"`} {
		if !strings.Contains(snap, want) {
			t.Errorf("latest snapshot missing %s:\n%s", want, snap)
		}
	}

	// Per-repo log has exactly one line for the single resolve.
	repoLog := filepath.Join(repo, ".weft", "resolve.log.jsonl")
	if got := countJSONLines(t, repoLog); got != 1 {
		t.Errorf("repo log lines = %d, want 1", got)
	}

	// The global rollup exists, has one line, and lives UNDER the isolated base —
	// the regression guard that --config keeps audit off the machine-wide dir.
	rollups := globalRollups(t)
	if len(rollups) != 1 {
		t.Fatalf("expected exactly one global rollup, got %v", rollups)
	}
	if !strings.HasPrefix(rollups[0], base) {
		t.Errorf("global rollup %q leaked outside isolated base %q", rollups[0], base)
	}
	if got := countJSONLines(t, rollups[0]); got != 1 {
		t.Errorf("global rollup lines = %d, want 1", got)
	}
}

// TestResolveRecord_DedupesIdenticalResolve proves re-running an unchanged
// resolve does not append a second log line (the resolution hash is stable),
// while the latest snapshot is still refreshed.
func TestResolveRecord_DedupesIdenticalResolve(t *testing.T) {
	base := withIsolatedConfig(t)
	twoSourceWorld(t, base)
	createProfile(t, "hybrid", "pers", "work")
	activate(t, "hybrid")
	repo := repoWith(t, "pom.xml")

	resolveWithRecord(t, repo)
	resolveWithRecord(t, repo) // identical selection

	repoLog := filepath.Join(repo, ".weft", "resolve.log.jsonl")
	if got := countJSONLines(t, repoLog); got != 1 {
		t.Errorf("identical re-resolve must not append; repo log lines = %d, want 1", got)
	}
	if got := countJSONLines(t, globalRollups(t)[0]); got != 1 {
		t.Errorf("identical re-resolve must not append to rollup; lines = %d, want 1", got)
	}
}

// TestResolveRecord_AppendsWhenSelectionChanges proves editing a loaded rule's
// body changes the resolution hash and appends a new audit line.
func TestResolveRecord_AppendsWhenSelectionChanges(t *testing.T) {
	base := withIsolatedConfig(t)
	twoSourceWorld(t, base)
	createProfile(t, "hybrid", "pers", "work")
	activate(t, "hybrid")
	repo := repoWith(t, "pom.xml")

	resolveWithRecord(t, repo)

	// Change the body of work's java rule — same label, new content → new hash.
	editRule(t, srcRulePath(base, "work", "java.md"), rule("java", "'pom.xml' in files", "WORK_JAVA_V2", "common"))

	resolveWithRecord(t, repo)

	repoLog := filepath.Join(repo, ".weft", "resolve.log.jsonl")
	if got := countJSONLines(t, repoLog); got != 2 {
		t.Errorf("changed body must append; repo log lines = %d, want 2", got)
	}
	if got := countJSONLines(t, globalRollups(t)[0]); got != 2 {
		t.Errorf("changed body must append to rollup; lines = %d, want 2", got)
	}
}
