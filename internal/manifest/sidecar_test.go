package manifest

import "testing"

func TestIsSidecarKey(t *testing.T) {
	cases := map[string]bool{
		"CLAUDE.md":                     false,
		"commands/foo.md":               false,
		"backend/java/rules":            false,
		"mcp:/home/you/.claude.json":    true,
		"mcp:C:/Users/you/.claude.json": true,
	}
	for key, want := range cases {
		if got := IsSidecarKey(key); got != want {
			t.Errorf("IsSidecarKey(%q) = %v, want %v", key, got, want)
		}
	}
}

// A manifest written before Staged existed falls back to the Files keys. Sidecar
// entries live in Files but were never staged, so the fallback must leave them
// out: apply would otherwise see one as dropped and resolve the sentinel against
// the target root, which merely fails to exist on Unix but is an invalid path on
// Windows, where the key embeds a drive colon.
func TestStagedSet_LegacyFallbackExcludesSidecarKeys(t *testing.T) {
	m := &Manifest{
		Files: map[string]string{
			"CLAUDE.md":                     "sha256:abc",
			"commands/foo.md":               "sha256:def",
			"mcp:/home/you/.claude.json":    "sha256:ghi",
			"mcp:C:/Users/you/.claude.json": "sha256:jkl",
		},
	}

	got := m.StagedSet()

	if len(got) != 2 {
		t.Fatalf("expected 2 staged keys, got %d: %v", len(got), got)
	}
	for key := range got {
		if IsSidecarKey(key) {
			t.Errorf("sidecar key %q leaked into the staged set", key)
		}
	}
}

// An explicit Staged list is authoritative and already excludes sidecars, so the
// filter must not be applied twice or otherwise alter it.
func TestStagedSet_ExplicitStagedUsedVerbatim(t *testing.T) {
	m := &Manifest{
		Staged: []string{"CLAUDE.md"},
		Files: map[string]string{
			"CLAUDE.md":                  "sha256:abc",
			"mcp:/home/you/.claude.json": "sha256:ghi",
		},
	}

	got := m.StagedSet()

	if len(got) != 1 {
		t.Fatalf("expected 1 staged key, got %d: %v", len(got), got)
	}
	if _, ok := got["CLAUDE.md"]; !ok {
		t.Errorf("expected CLAUDE.md in the staged set, got %v", got)
	}
}
