package pathlint

import (
	"os"
	"path/filepath"
	"testing"
)

// setupSource builds a temp source tree and returns its root.
func setupSource(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func findingFor(findings []Finding, ref string) (Finding, bool) {
	for _, f := range findings {
		if f.Ref == ref {
			return f, true
		}
	}
	return Finding{}, false
}

func TestScanClassifies(t *testing.T) {
	root := setupSource(t, map[string]string{
		"common/code-review.md": "# review rules",
		"CLAUDE.md": "" +
			"@~/oldpath/common/code-review.md\n" + // stale-prefix (unique suffix match)
			"@{{weft.root}}/nope/missing.md\n" + // broken-anchor
			"{{weft.source:ghost}}/x.md\n" + // unresolved-anchor
			"@~/nowhere/zzz.md\n" + // dead-reference
			"@{{weft.root}}/common/code-review.md\n", // fine — anchored + resolves
	})

	findings, err := Scan([]Source{{Name: "work", Root: root}})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	stale, ok := findingFor(findings, "@~/oldpath/common/code-review.md")
	if !ok || stale.Kind != StalePrefix {
		t.Fatalf("expected stale-prefix, got %+v (ok=%v)", stale, ok)
	}
	if stale.Suggestion != "@{{weft.root}}/common/code-review.md" {
		t.Errorf("stale suggestion = %q", stale.Suggestion)
	}

	if f, ok := findingFor(findings, "@{{weft.root}}/nope/missing.md"); !ok || f.Kind != BrokenAnchor {
		t.Errorf("expected broken-anchor, got %+v (ok=%v)", f, ok)
	}
	if f, ok := findingFor(findings, "{{weft.source:ghost}}/x.md"); !ok || f.Kind != UnresolvedAnchor {
		t.Errorf("expected unresolved-anchor, got %+v (ok=%v)", f, ok)
	}
	if f, ok := findingFor(findings, "@~/nowhere/zzz.md"); !ok || f.Kind != DeadReference {
		t.Errorf("expected dead-reference, got %+v (ok=%v)", f, ok)
	}
	// The already-anchored, resolvable reference must not be reported.
	if f, ok := findingFor(findings, "@{{weft.root}}/common/code-review.md"); ok {
		t.Errorf("resolvable anchor should not be a finding, got %+v", f)
	}
}

func TestHardcodedInSourceAndCrossSource(t *testing.T) {
	work := setupSource(t, map[string]string{
		"java/x.md": "java rules",
	})
	team := setupSource(t, map[string]string{
		"CLAUDE.md": "see " + filepath.Join(work, "java", "x.md") + " for details\n",
	})

	findings, err := Scan([]Source{{Name: "team", Root: team}, {Name: "work", Root: work}})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	ref := filepath.Join(work, "java", "x.md")
	f, ok := findingFor(findings, ref)
	if !ok || f.Kind != HardcodedInSource {
		t.Fatalf("expected hardcoded-in-source, got %+v (ok=%v)", f, ok)
	}
	// The file lives in "team" but the target is in "work" → cross-source anchor.
	if f.Suggestion != "{{weft.source:work}}/java/x.md" {
		t.Errorf("cross-source suggestion = %q", f.Suggestion)
	}
}

func TestApplyRewrites(t *testing.T) {
	root := setupSource(t, map[string]string{
		"common/code-review.md": "# rules",
		// Malformed "@.~/" prefix must heal cleanly (no stray ".").
		"CLAUDE.md": "@.~/oldpath/common/code-review.md\n",
	})
	findings, err := Scan([]Source{{Name: "work", Root: root}})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	changed, err := Apply(findings)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if changed != 1 {
		t.Errorf("changed = %d, want 1", changed)
	}
	got, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	want := "@{{weft.root}}/common/code-review.md\n"
	if string(got) != want {
		t.Errorf("rewritten file = %q, want %q", got, want)
	}

	// Idempotent: a second scan finds nothing to heal (the anchor resolves).
	findings2, _ := Scan([]Source{{Name: "work", Root: root}})
	for _, f := range findings2 {
		if f.Fixable() {
			t.Errorf("unexpected healable finding after fix: %+v", f)
		}
	}
}
