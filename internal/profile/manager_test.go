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
