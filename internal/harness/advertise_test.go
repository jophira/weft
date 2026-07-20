package harness

import (
	"path/filepath"
	"strings"
	"testing"
)

// seedAdvertiseTree builds a staged tree covering every advertisable class.
func seedAdvertiseTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "CLAUDE.md"), "rules")
	write(t, filepath.Join(root, "commands", "review.md"),
		"---\nname: review\ndescription: senior code review\n---\nbody")
	write(t, filepath.Join(root, "agents", "explorer.md"),
		"---\nname: explorer\ndescription: read-only search agent\n---\nbody")
	write(t, filepath.Join(root, "skills", "graphify", "SKILL.md"),
		"---\nname: graphify\ndescription: knowledge graph builder\n---\nbody")
	// A skill asset, not an entry point — must not appear in the index.
	write(t, filepath.Join(root, "skills", "graphify", "style.css"), "body{}")
	return root
}

func TestScanAdvertised_IndexesUnsupportedProseClasses(t *testing.T) {
	root := seedAdvertiseTree(t)

	entries, err := scanAdvertised(root, &Codex{})
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]advertised{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	// Codex executes commands natively from prompts/, so they need no pointer.
	if _, ok := byName["review"]; ok {
		t.Error("commands are natively supported by Codex and must not be advertised")
	}
	if _, ok := byName["explorer"]; !ok {
		t.Error("agents have no Codex analogue and should be advertised")
	}
	if got, ok := byName["graphify"]; !ok {
		t.Error("skills should be advertised")
	} else if got.Desc != "knowledge graph builder" {
		t.Errorf("description = %q, want %q", got.Desc, "knowledge graph builder")
	}
	if _, ok := byName["style"]; ok {
		t.Error("non-markdown skill assets are not entry points and must not be indexed")
	}
}

// Claude Code executes every class natively, so it has nothing to advertise.
func TestScanAdvertised_NothingWhenAllClassesNative(t *testing.T) {
	entries, err := scanAdvertised(seedAdvertiseTree(t), &ClaudeCode{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no advertised entries, got %d", len(entries))
	}
}

// An agent restricting `tools:` depends on the harness enforcing that limit.
// Only Claude Code can, so advertising it elsewhere would silently widen its
// access — withhold it instead.
func TestScanAdvertised_ExcludesToolRestrictedAgents(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "agents", "readonly.md"),
		"---\nname: readonly\ndescription: read-only reviewer\ntools: Read, Grep\n---\nbody")
	write(t, filepath.Join(root, "agents", "open.md"),
		"---\nname: open\ndescription: general agent\n---\nbody")

	entries, err := scanAdvertised(root, &Codex{})
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if e.Name == "readonly" {
			t.Error("tool-restricted agent must not be advertised to a harness that cannot enforce tools:")
		}
	}
	if len(entries) != 1 || entries[0].Name != "open" {
		t.Errorf("expected only the unrestricted agent, got %+v", entries)
	}
}

func TestScanAdvertised_FallsBackToPathNames(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "agents", "no-header.md"), "just a body")
	write(t, filepath.Join(root, "skills", "bundled", "SKILL.md"), "no header either")

	entries, err := scanAdvertised(root, &Cursor{})
	if err != nil {
		t.Fatal(err)
	}

	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["no-header"] {
		t.Error("agent without frontmatter should fall back to its filename")
	}
	// Skills are identified by bundle directory, not the constant "SKILL".
	if !names["bundled"] {
		t.Errorf("skill without frontmatter should fall back to its directory name, got %v", names)
	}
}

// A reordering index would rewrite the instruction file on every apply and churn
// the watcher, so ordering must be deterministic.
func TestScanAdvertised_StableOrder(t *testing.T) {
	root := seedAdvertiseTree(t)
	first, err := scanAdvertised(root, &Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := scanAdvertised(root, &Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("entry count differs between scans: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Name != second[i].Name || first[i].Class != second[i].Class {
			t.Fatalf("order differs at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestAdvertiseBody_RendersGroupedIndex(t *testing.T) {
	body := advertiseBody([]advertised{
		{Class: ClassAgents, Name: "explorer", Desc: "read-only search", Path: "/w/agents/explorer.md"},
		{Class: ClassSkills, Name: "graphify", Desc: "graph builder", Path: "/w/skills/graphify/SKILL.md"},
	})

	for _, want := range []string{"### agents", "### skills", "explorer", "graphify", "/w/agents/explorer.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "not loaded automatically") {
		t.Error("index must state that entries are read on demand, not auto-loaded")
	}
}

func TestAdvertiseBody_EmptyWhenNothingToAdvertise(t *testing.T) {
	if got := advertiseBody(nil); got != "" {
		t.Errorf("expected empty body, got %q", got)
	}
}

// The index must stay cheap enough to live in an always-on rule file.
func TestTruncate(t *testing.T) {
	long := strings.Repeat("x", maxDescLen+50)
	got := truncate(long, maxDescLen)
	if len([]rune(got)) > maxDescLen+1 { // +1 for the ellipsis
		t.Errorf("truncate returned %d runes, want <= %d", len([]rune(got)), maxDescLen+1)
	}
	if got := truncate("has\nnewline", maxDescLen); strings.Contains(got, "\n") {
		t.Errorf("newlines must be flattened to keep one entry per line, got %q", got)
	}
	// Multi-byte runes must not be split mid-sequence.
	if got := truncate(strings.Repeat("é", maxDescLen+10), maxDescLen); !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis on truncated multi-byte string, got %q", got)
	}
}
