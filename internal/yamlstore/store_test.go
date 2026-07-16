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
	stray := filepath.Join(filepath.Dir(s.FilePath("cog")), "notes.txt")
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
	if got := s.FilePath("cog"); got != want {
		t.Fatalf("FilePath: got %q, want %q", got, want)
	}
}
