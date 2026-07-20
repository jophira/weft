package harness

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestStagedClass(t *testing.T) {
	tests := []struct {
		rel  string
		want Class
	}{
		{"CLAUDE.md", ClassInstructions},
		{"claude.md", ClassInstructions}, // matched case-insensitively
		{"README.md", ClassOther},
		{"commands/review.md", ClassCommands},
		{"agents/reviewer.md", ClassAgents},
		{"skills/graphify/SKILL.md", ClassSkills},
		{"skills/proposal/assets/logo.png", ClassSkills},
		{"hooks/pre.sh", ClassOther},
		{filepath.Join("commands", "nested", "deep.md"), ClassCommands},
	}
	for _, tt := range tests {
		t.Run(tt.rel, func(t *testing.T) {
			if got := stagedClass(tt.rel); got != tt.want {
				t.Errorf("stagedClass(%q) = %q, want %q", tt.rel, got, tt.want)
			}
		})
	}
}

func TestRetarget(t *testing.T) {
	tests := []struct {
		name    string
		rel     string
		class   Class
		support ClassSupport
		want    string
		wantOK  bool
	}{
		{
			name:    "relocates to harness subdir",
			rel:     "commands/review.md",
			class:   ClassCommands,
			support: ClassSupport{Placement: PlacementNative, SubDir: "prompts"},
			want:    filepath.FromSlash("prompts/review.md"),
			wantOK:  true,
		},
		{
			name:    "preserves nesting under the new subdir",
			rel:     filepath.FromSlash("skills/graphify/SKILL.md"),
			class:   ClassSkills,
			support: ClassSupport{Placement: PlacementNative, SubDir: "abilities"},
			want:    filepath.FromSlash("abilities/graphify/SKILL.md"),
			wantOK:  true,
		},
		{
			name:    "empty subdir keeps the staged path",
			rel:     filepath.FromSlash("skills/graphify/SKILL.md"),
			class:   ClassSkills,
			support: ClassSupport{Placement: PlacementNative},
			want:    filepath.FromSlash("skills/graphify/SKILL.md"),
			wantOK:  true,
		},
		{
			name:    "unsupported class is not routed anywhere",
			rel:     "agents/reviewer.md",
			class:   ClassAgents,
			support: ClassSupport{Placement: PlacementNone},
			wantOK:  false,
		},
		{
			name:    "advertising does not make a class writable",
			rel:     "agents/reviewer.md",
			class:   ClassAgents,
			support: ClassSupport{Placement: PlacementNone, Advertise: true},
			wantOK:  false,
		},
		{
			name:    "unclassified files keep their staged path",
			rel:     "hooks/pre.sh",
			class:   ClassOther,
			support: ClassSupport{Placement: PlacementNative, SubDir: "ignored"},
			want:    "hooks/pre.sh",
			wantOK:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := retarget(tt.rel, tt.class, tt.support)
			if ok != tt.wantOK {
				t.Fatalf("retarget ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("retarget = %q, want %q", got, tt.want)
			}
		})
	}
}

// An explicit rename is how the root instruction file gets its per-harness name.
// It predates the class model and must keep winning over class routing.
func TestRouteStaged_RenameWins(t *testing.T) {
	renames := map[string]string{"CLAUDE.md": "AGENTS.md"}
	got, ok := routeStaged("CLAUDE.md", renames, &Codex{})
	if !ok || got != "AGENTS.md" {
		t.Errorf("routeStaged = (%q, %v), want (AGENTS.md, true)", got, ok)
	}
}

// A nil harness must behave exactly as weft did before the class model, so
// callers with no Harness value (Warp's re-rooted tree, ad-hoc applies) are
// unaffected.
func TestRouteStaged_NilHarnessKeepsStagedPath(t *testing.T) {
	for _, rel := range []string{"commands/review.md", "agents/a.md", "CLAUDE.md"} {
		got, ok := routeStaged(rel, nil, nil)
		if !ok || got != rel {
			t.Errorf("routeStaged(%q, nil, nil) = (%q, %v), want (%q, true)", rel, got, ok, rel)
		}
	}
}

func TestRouteStaged_PerHarnessPlacement(t *testing.T) {
	tests := []struct {
		harness Harness
		rel     string
		want    string
		wantOK  bool
	}{
		{&ClaudeCode{}, "commands/review.md", filepath.FromSlash("commands/review.md"), true},
		{&ClaudeCode{}, "agents/reviewer.md", filepath.FromSlash("agents/reviewer.md"), true},
		{&Codex{}, "commands/review.md", filepath.FromSlash("prompts/review.md"), true},
		{&Codex{}, "agents/reviewer.md", "", false},
		{&Codex{}, "skills/x/SKILL.md", "", false},
		{&Cursor{}, "commands/review.md", "", false},
		{&GeminiCLI{}, "commands/review.md", "", false}, // TOML format gap, not just a path gap
		{&Windsurf{}, "agents/reviewer.md", "", false},
		{&Aider{}, "skills/x/SKILL.md", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.harness.Name()+"/"+tt.rel, func(t *testing.T) {
			got, ok := routeStaged(tt.rel, nil, tt.harness)
			if ok != tt.wantOK {
				t.Fatalf("routeStaged ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("routeStaged = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseClass(t *testing.T) {
	if c, ok := ParseClass(" Agents "); !ok || c != ClassAgents {
		t.Errorf("ParseClass(\" Agents \") = (%q, %v), want (agents, true)", c, ok)
	}
	if _, ok := ParseClass("hooks"); ok {
		t.Error("ParseClass(\"hooks\") should report unknown")
	}
}

// The skipped report is the user's only signal that a class was dropped, so it
// must name the class and distinguish "advertised" from "nowhere to put it".
func TestReportSkipped(t *testing.T) {
	var buf bytes.Buffer
	reportSkipped(&buf, map[Class]int{ClassAgents: 2, ClassMCP: 1}, &Codex{})
	got := buf.String()

	if !bytes.Contains(buf.Bytes(), []byte("2 agents file(s)")) {
		t.Errorf("missing agents count in %q", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("advertised in the instruction index")) {
		t.Errorf("agents are advertised by Codex and should say so: %q", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("no native location")) {
		t.Errorf("mcp is not advertised and should say so: %q", got)
	}
}

func TestReportSkipped_SilentWhenNothingSkipped(t *testing.T) {
	var buf bytes.Buffer
	reportSkipped(&buf, map[Class]int{}, &Codex{})
	if buf.Len() != 0 {
		t.Errorf("expected no output, got %q", buf.String())
	}
}
