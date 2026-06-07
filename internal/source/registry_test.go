package source_test

import (
	"testing"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/source"
)

// newReg returns a FileRegistry backed by a temporary directory that is
// automatically cleaned up when the test ends.
func newReg(t *testing.T) *source.FileRegistry {
	t.Helper()
	return source.NewFileRegistry(t.TempDir())
}

func fixture(name string) source.Source {
	return source.Source{
		Name:      name,
		Root:      "/tmp/rules-" + name,
		Remote:    "git@github.com:test/" + name + ".git",
		Branch:    "main",
		AutoPull:  true,
		Structure: source.DefaultStructure(),
	}
}

// ── Add ──────────────────────────────────────────────────────────────────────

func TestAdd_persists(t *testing.T) {
	r := newReg(t)
	if err := r.Add(fixture("work")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := r.Get("work")
	if err != nil {
		t.Fatalf("Get after Add: %v", err)
	}
	if got.Name != "work" {
		t.Errorf("Name = %q, want %q", got.Name, "work")
	}
	if got.Remote != "git@github.com:test/work.git" {
		t.Errorf("Remote = %q, unexpected", got.Remote)
	}
}

func TestAdd_defaultBranch(t *testing.T) {
	r := newReg(t)
	s := fixture("work")
	s.Branch = ""
	_ = r.Add(s)
	got, _ := r.Get("work")
	if got.Branch != "main" {
		t.Errorf("Branch = %q, want %q", got.Branch, "main")
	}
}

func TestAdd_duplicateReturnsError(t *testing.T) {
	r := newReg(t)
	_ = r.Add(fixture("work"))
	if err := r.Add(fixture("work")); err == nil {
		t.Fatal("expected error on duplicate Add, got nil")
	}
}

func TestAdd_invalidNames(t *testing.T) {
	r := newReg(t)
	bad := []string{"", "My Source", "123start", "has/slash", "Has-Upper", "a b"}
	for _, name := range bad {
		s := fixture(name)
		if err := r.Add(s); err == nil {
			t.Errorf("Add(%q): expected validation error, got nil", name)
		}
	}
}

func TestAdd_validNames(t *testing.T) {
	r := newReg(t)
	good := []string{"work", "my-team", "personal2", "x", "a_b_c"}
	for _, name := range good {
		if err := r.Add(fixture(name)); err != nil {
			t.Errorf("Add(%q): unexpected error: %v", name, err)
		}
	}
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestGet_notFound(t *testing.T) {
	r := newReg(t)
	if _, err := r.Get("nonexistent"); err == nil {
		t.Fatal("expected error for missing source, got nil")
	}
}

// ── Remove ────────────────────────────────────────────────────────────────────

func TestRemove_deletesSource(t *testing.T) {
	r := newReg(t)
	_ = r.Add(fixture("work"))
	if err := r.Remove("work"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := r.Get("work"); err == nil {
		t.Fatal("expected error after Remove, got nil")
	}
}

func TestRemove_notFound(t *testing.T) {
	r := newReg(t)
	if err := r.Remove("nonexistent"); err == nil {
		t.Fatal("expected error removing missing source, got nil")
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestList_empty(t *testing.T) {
	r := newReg(t)
	sources, err := r.List()
	if err != nil {
		t.Fatalf("List on empty dir: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("expected empty list, got %d", len(sources))
	}
}

func TestList_returnsAll(t *testing.T) {
	r := newReg(t)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := r.Add(fixture(name)); err != nil {
			t.Fatalf("Add(%q): %v", name, err)
		}
	}
	sources, err := r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sources) != 3 {
		t.Errorf("List returned %d sources, want 3", len(sources))
	}
}

// ── Path helpers ──────────────────────────────────────────────────────────────
// These tests now exercise locate.ExpandHome / locate.Tilde directly, as the
// duplicate implementations were removed from the source package (issue #97).

func TestContractHome_roundtrip(t *testing.T) {
	original := "~/.claude"
	expanded := locate.ExpandHome(original)
	contracted := locate.Tilde(expanded)
	if contracted != original {
		t.Errorf("Tilde(ExpandHome(%q)) = %q, want original", original, contracted)
	}
}

func TestExpandHome_noTilde(t *testing.T) {
	path := "/absolute/path"
	if got := locate.ExpandHome(path); got != path {
		t.Errorf("ExpandHome(%q) = %q, want unchanged", path, got)
	}
}
