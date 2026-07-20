package cmd

import (
	"strings"
	"testing"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/profile"
)

// A profile written before harness_sync existed must keep projecting exactly
// what it projected before, so an absent entry has to mean "unrestricted".
func TestAllowedClasses_AbsentEntryIsUnrestricted(t *testing.T) {
	p := &profile.Profile{Name: "p"}

	got, err := allowedClasses(p, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil (unrestricted), got %v", got)
	}
}

// An explicit empty list is a deliberate "project nothing" and must be
// distinguishable from an absent key, or disabling a harness would be
// indistinguishable from forgetting to configure it.
func TestAllowedClasses_ExplicitEmptyProjectsNothing(t *testing.T) {
	p := &profile.Profile{
		Name:        "p",
		HarnessSync: profile.HarnessSync{"cursor": {}},
	}

	got, err := allowedClasses(p, "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("explicit empty list must yield a non-nil map, not unrestricted")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestAllowedClasses_ParsesConfiguredClasses(t *testing.T) {
	p := &profile.Profile{
		Name:        "p",
		HarnessSync: profile.HarnessSync{"codex": {"instructions", "commands"}},
	}

	got, err := allowedClasses(p, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !got[harness.ClassInstructions] || !got[harness.ClassCommands] {
		t.Errorf("configured classes missing from %v", got)
	}
	if got[harness.ClassAgents] {
		t.Error("agents was not configured and must not be allowed")
	}
}

// A typo silently disabling a class would be the worst outcome, so an unknown
// class name must fail loudly and name the valid options.
func TestAllowedClasses_UnknownClassIsAnError(t *testing.T) {
	p := &profile.Profile{
		Name:        "p",
		HarnessSync: profile.HarnessSync{"codex": {"instructions", "hooks"}},
	}

	_, err := allowedClasses(p, "codex")
	if err == nil {
		t.Fatal("expected an error for an unknown class")
	}
	for _, want := range []string{"hooks", "codex", "instructions"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// Config is per harness: restricting one must not affect its neighbours.
func TestAllowedClasses_IsPerHarness(t *testing.T) {
	p := &profile.Profile{
		Name:        "p",
		HarnessSync: profile.HarnessSync{"cursor": {"instructions"}},
	}

	restricted, err := allowedClasses(p, "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if len(restricted) != 1 {
		t.Errorf("cursor should be restricted to one class, got %v", restricted)
	}

	other, err := allowedClasses(p, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if other != nil {
		t.Errorf("claude-code has no entry and must stay unrestricted, got %v", other)
	}
}

func TestHarnessSync_ClassesForDistinguishesAbsentFromEmpty(t *testing.T) {
	hs := profile.HarnessSync{"cursor": {}}

	if _, ok := hs.ClassesFor("cursor"); !ok {
		t.Error("an explicit empty list must report as configured")
	}
	if _, ok := hs.ClassesFor("codex"); ok {
		t.Error("an absent key must report as not configured")
	}

	var nilSync profile.HarnessSync
	if _, ok := nilSync.ClassesFor("codex"); ok {
		t.Error("a nil HarnessSync must report nothing as configured")
	}
}
