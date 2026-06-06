package profile_test

import (
	"testing"

	"github.com/jophira/weft/internal/profile"
)

func newMgr(t *testing.T) *profile.FileManager {
	t.Helper()
	return profile.NewFileManager(t.TempDir())
}

func fixture(name string) profile.Profile {
	return profile.Profile{
		Name:         name,
		Sources:      []string{"work", "personal"},
		Overlay:      profile.OverlayCascade,
		ActiveTarget: "claude-code",
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestCreate_persists(t *testing.T) {
	m := newMgr(t)
	if err := m.Create(fixture("hybrid")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := m.Get("hybrid")
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	if got.Name != "hybrid" {
		t.Errorf("Name = %q, want %q", got.Name, "hybrid")
	}
	if len(got.Sources) != 2 {
		t.Errorf("Sources len = %d, want 2", len(got.Sources))
	}
	if got.Overlay != profile.OverlayCascade {
		t.Errorf("Overlay = %q, want %q", got.Overlay, profile.OverlayCascade)
	}
}

func TestCreate_defaultOverlay(t *testing.T) {
	m := newMgr(t)
	p := fixture("hybrid")
	p.Overlay = ""
	if err := m.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := m.Get("hybrid")
	if got.Overlay != profile.OverlayCascade {
		t.Errorf("Overlay = %q, want default %q", got.Overlay, profile.OverlayCascade)
	}
}

func TestCreate_allOverlays(t *testing.T) {
	overlays := []profile.Overlay{
		profile.OverlayCascade,
		profile.OverlayMerge,
		profile.OverlayLastWins,
	}
	for _, o := range overlays {
		m := newMgr(t)
		p := fixture("p")
		p.Overlay = o
		if err := m.Create(p); err != nil {
			t.Errorf("Create with overlay %q: %v", o, err)
		}
	}
}

func TestCreate_duplicateReturnsError(t *testing.T) {
	m := newMgr(t)
	_ = m.Create(fixture("hybrid"))
	if err := m.Create(fixture("hybrid")); err == nil {
		t.Fatal("expected error on duplicate Create, got nil")
	}
}

func TestCreate_invalidNames(t *testing.T) {
	m := newMgr(t)
	bad := []string{"", "My Profile", "123start", "has/slash", "Has-Upper"}
	for _, name := range bad {
		p := fixture(name)
		if err := m.Create(p); err == nil {
			t.Errorf("Create(%q): expected validation error, got nil", name)
		}
	}
}

func TestCreate_emptySources(t *testing.T) {
	m := newMgr(t)
	p := fixture("hybrid")
	p.Sources = nil
	if err := m.Create(p); err == nil {
		t.Fatal("expected error for empty sources, got nil")
	}
}

func TestCreate_invalidOverlay(t *testing.T) {
	m := newMgr(t)
	p := fixture("hybrid")
	p.Overlay = "wrong"
	if err := m.Create(p); err == nil {
		t.Fatal("expected error for invalid overlay, got nil")
	}
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestGet_notFound(t *testing.T) {
	m := newMgr(t)
	if _, err := m.Get("nonexistent"); err == nil {
		t.Fatal("expected error for missing profile, got nil")
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestDelete_removes(t *testing.T) {
	m := newMgr(t)
	_ = m.Create(fixture("hybrid"))
	if err := m.Delete("hybrid"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Get("hybrid"); err == nil {
		t.Fatal("expected error after Delete, got nil")
	}
}

func TestDelete_notFound(t *testing.T) {
	m := newMgr(t)
	if err := m.Delete("nonexistent"); err == nil {
		t.Fatal("expected error deleting missing profile, got nil")
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestList_empty(t *testing.T) {
	m := newMgr(t)
	profiles, err := m.List()
	if err != nil {
		t.Fatalf("List on empty dir: %v", err)
	}
	if len(profiles) != 0 {
		t.Errorf("expected empty list, got %d", len(profiles))
	}
}

func TestList_returnsAll(t *testing.T) {
	m := newMgr(t)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		p := fixture(name)
		if err := m.Create(p); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}
	profiles, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(profiles) != 3 {
		t.Errorf("List returned %d profiles, want 3", len(profiles))
	}
}

func TestList_preservesSources(t *testing.T) {
	m := newMgr(t)
	p := fixture("hybrid")
	p.Sources = []string{"work", "personal", "community"}
	_ = m.Create(p)

	profiles, _ := m.List()
	if len(profiles[0].Sources) != 3 {
		t.Errorf("Sources len = %d, want 3", len(profiles[0].Sources))
	}
}

// ── WriteBack ─────────────────────────────────────────────────────────────────

func TestCreate_writeBackOmitted(t *testing.T) {
	m := newMgr(t)
	if err := m.Create(fixture("hybrid")); err != nil {
		t.Fatalf("Create without write_back: %v", err)
	}
}

func TestCreate_writeBackDefaultValid(t *testing.T) {
	m := newMgr(t)
	p := fixture("hybrid") // sources: work, personal
	p.WriteBack = profile.WriteBack{Default: "work"}
	if err := m.Create(p); err != nil {
		t.Fatalf("Create with valid write_back.default: %v", err)
	}
}

func TestCreate_writeBackDefaultInvalid(t *testing.T) {
	m := newMgr(t)
	p := fixture("hybrid")
	p.WriteBack = profile.WriteBack{Default: "unknown-source"}
	if err := m.Create(p); err == nil {
		t.Fatal("expected error for write_back.default not in sources, got nil")
	}
}

func TestCreate_writeBackOverrideValid(t *testing.T) {
	m := newMgr(t)
	p := fixture("hybrid") // sources: work, personal
	p.WriteBack = profile.WriteBack{
		Default:   "work",
		Overrides: map[string]string{"CLAUDE.md": "personal"},
	}
	if err := m.Create(p); err != nil {
		t.Fatalf("Create with valid write_back.overrides: %v", err)
	}
}

func TestCreate_writeBackOverrideInvalid(t *testing.T) {
	m := newMgr(t)
	p := fixture("hybrid")
	p.WriteBack = profile.WriteBack{
		Default:   "work",
		Overrides: map[string]string{"CLAUDE.md": "ghost-source"},
	}
	if err := m.Create(p); err == nil {
		t.Fatal("expected error for write_back.overrides value not in sources, got nil")
	}
}

func TestCreate_writeBackRoundTrip(t *testing.T) {
	m := newMgr(t)
	p := fixture("hybrid")
	p.WriteBack = profile.WriteBack{
		Default:   "personal",
		Overrides: map[string]string{"CLAUDE.md": "work"},
	}
	if err := m.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := m.Get("hybrid")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.WriteBack.Default != "personal" {
		t.Errorf("WriteBack.Default = %q, want %q", got.WriteBack.Default, "personal")
	}
	if got.WriteBack.Overrides["CLAUDE.md"] != "work" {
		t.Errorf("WriteBack.Overrides[CLAUDE.md] = %q, want %q", got.WriteBack.Overrides["CLAUDE.md"], "work")
	}
}

// ── ResolvedTargets ───────────────────────────────────────────────────────────

func TestResolvedTargets_targetsField(t *testing.T) {
	p := profile.Profile{Targets: []string{"claude-code", "codex"}}
	got := p.ResolvedTargets()
	if len(got) != 2 || got[0] != "claude-code" || got[1] != "codex" {
		t.Errorf("ResolvedTargets = %v, want [claude-code codex]", got)
	}
}

func TestResolvedTargets_activeTargetFallback(t *testing.T) {
	p := profile.Profile{ActiveTarget: "claude-code"}
	got := p.ResolvedTargets()
	if len(got) != 1 || got[0] != "claude-code" {
		t.Errorf("ResolvedTargets = %v, want [claude-code]", got)
	}
}

func TestResolvedTargets_targetsPreferredOverActiveTarget(t *testing.T) {
	p := profile.Profile{
		ActiveTarget: "claude-code",
		Targets:      []string{"codex", "cursor"},
	}
	got := p.ResolvedTargets()
	if len(got) != 2 || got[0] != "codex" || got[1] != "cursor" {
		t.Errorf("ResolvedTargets = %v, want [codex cursor]", got)
	}
}

func TestResolvedTargets_neitherFieldReturnsNil(t *testing.T) {
	p := profile.Profile{}
	if got := p.ResolvedTargets(); got != nil {
		t.Errorf("ResolvedTargets = %v, want nil", got)
	}
}

// ── Backwards-compat: old active_target YAML loads correctly ─────────────────

func TestBackwardsCompat_activeTargetLoadsViaResolvedTargets(t *testing.T) {
	m := newMgr(t)
	// fixture sets ActiveTarget: "claude-code" and no Targets field
	if err := m.Create(fixture("legacy")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := m.Get("legacy")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Targets field should be empty (old format)
	if len(got.Targets) != 0 {
		t.Errorf("Targets = %v, want empty for legacy profile", got.Targets)
	}
	// But ResolvedTargets falls back to ActiveTarget
	resolved := got.ResolvedTargets()
	if len(resolved) != 1 || resolved[0] != "claude-code" {
		t.Errorf("ResolvedTargets = %v, want [claude-code]", resolved)
	}
}

func TestCreate_targetsRoundTrip(t *testing.T) {
	m := newMgr(t)
	p := profile.Profile{
		Name:    "multi",
		Sources: []string{"work", "personal"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"claude-code", "codex"},
	}
	if err := m.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := m.Get("multi")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Targets) != 2 || got.Targets[0] != "claude-code" || got.Targets[1] != "codex" {
		t.Errorf("Targets = %v, want [claude-code codex]", got.Targets)
	}
	// ResolvedTargets should return Targets field
	resolved := got.ResolvedTargets()
	if len(resolved) != 2 {
		t.Errorf("ResolvedTargets len = %d, want 2", len(resolved))
	}
}
