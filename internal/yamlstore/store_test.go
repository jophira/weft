package yamlstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type widget struct {
	Name  string `yaml:"name"`
	Color string `yaml:"color"`
}

func newStore(t *testing.T) *Store[widget] {
	t.Helper()
	return New[widget](filepath.Join(t.TempDir(), "widgets"))
}

func TestWrite_thenGet_roundTrips(t *testing.T) {
	s := newStore(t)
	w := widget{Name: "cog", Color: "red"}
	if err := s.Write(w.Name, w); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := s.Get("cog")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if *got != w {
		t.Fatalf("got %+v, want %+v", *got, w)
	}
}

func TestWrite_createsDirectory(t *testing.T) {
	s := newStore(t)
	if err := s.Write("cog", widget{Name: "cog"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !s.Exists("cog") {
		t.Fatal("expected cog to exist after Write")
	}
}

func TestGet_missingReturnsErrNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get: got %v, want ErrNotFound", err)
	}
}

func TestExists_falseBeforeWrite(t *testing.T) {
	s := newStore(t)
	if s.Exists("cog") {
		t.Fatal("expected cog to not exist yet")
	}
}

func TestRemove_removesRecord(t *testing.T) {
	s := newStore(t)
	if err := s.Write("cog", widget{Name: "cog"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Remove("cog"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if s.Exists("cog") {
		t.Fatal("expected cog to be gone after Remove")
	}
}

func TestRemove_missingReturnsErrNotFound(t *testing.T) {
	s := newStore(t)
	if err := s.Remove("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Remove: got %v, want ErrNotFound", err)
	}
}

func TestList_emptyOnMissingDirectory(t *testing.T) {
	s := newStore(t)
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List: got %d entries, want 0", len(got))
	}
}

func TestList_returnsAllSortedByFilename(t *testing.T) {
	s := newStore(t)
	for _, name := range []string{"bravo", "alpha", "charlie"} {
		if err := s.Write(name, widget{Name: name}); err != nil {
			t.Fatalf("Write(%s): %v", name, err)
		}
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List: got %d entries, want 3", len(got))
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, w := range want {
		if got[i].Name != w {
			t.Fatalf("List[%d]: got %q, want %q", i, got[i].Name, w)
		}
	}
}

func TestList_skipsNonYAMLFiles(t *testing.T) {
	s := newStore(t)
	if err := s.Write("cog", widget{Name: "cog"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	stray := filepath.Join(filepath.Dir(s.filePath("cog")), "notes.txt")
	if err := os.WriteFile(stray, []byte("hello"), 0o644); err != nil {
		t.Fatalf("writing stray file: %v", err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List: got %d entries, want 1 (stray file should be skipped)", len(got))
	}
}

func TestFilePath_joinsDirAndName(t *testing.T) {
	dir := filepath.Join("tmp", "example")
	s := New[widget](dir)
	want := filepath.Join(dir, "cog.yaml")
	if got := s.filePath("cog"); got != want {
		t.Fatalf("FilePath: got %q, want %q", got, want)
	}
}

// ── name validation on every operation (#277) ────────────────────────────────

// The name becomes a filename. Every operation that builds a path from a
// caller-supplied name must reject a traversing one, not just the create path.
func TestOperations_rejectTraversingNames(t *testing.T) {
	bad := []string{
		"../escape",
		"../../etc/passwd",
		"nested/name",
		`..\windows`,
		"/absolute",
		".",
		"..",
		"",
		"Upper",
		"has space",
		"trailing/",
	}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			if err := s.Write(name, widget{Name: "x"}); err == nil {
				t.Error("Write accepted the name")
			}
			if _, err := s.Get(name); err == nil {
				t.Error("Get accepted the name")
			}
			if err := s.Remove(name); err == nil {
				t.Error("Remove accepted the name")
			}
			if s.Exists(name) {
				t.Error("Exists reported true")
			}
		})
	}
}

// The escape has to be refused, not merely fail to find anything: a traversing
// name that happens to point at a real file must not read or delete it.
func TestGetRemove_traversingNameCannotReachAFileOutsideTheStore(t *testing.T) {
	s := newStore(t)
	if err := s.Write("cog", widget{Name: "cog"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	outside := filepath.Join(filepath.Dir(filepath.Dir(s.filePath("cog"))), "secret.yaml")
	if err := os.WriteFile(outside, []byte("name: secret\n"), 0o644); err != nil {
		t.Fatalf("writing the outside file: %v", err)
	}

	if _, err := s.Get("../secret"); err == nil {
		t.Error("Get read a file outside the store")
	}
	if err := s.Remove("../secret"); err == nil {
		t.Error("Remove deleted a file outside the store")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the file outside the store was removed: %v", err)
	}
}

// List reads names back out of the store's own directory rather than taking
// them from a caller, so a file that predates the rule still lists.
func TestList_keepsNamesThatPredateTheRule(t *testing.T) {
	s := newStore(t)
	if err := s.Write("cog", widget{Name: "cog"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	legacy := filepath.Join(filepath.Dir(s.filePath("cog")), "Legacy.yaml")
	if err := os.WriteFile(legacy, []byte("name: legacy\n"), 0o644); err != nil {
		t.Fatalf("writing the legacy file: %v", err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List returned %d entries, want 2 — a pre-existing name should still list", len(got))
	}
}
