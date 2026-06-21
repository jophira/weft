package source_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/source"
)

// names extracts the ordered source names for concise assertions.
func names(srcs []source.Source) []string {
	out := make([]string, len(srcs))
	for i, s := range srcs {
		out[i] = s.Name
	}
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSortByPriority_highestEmittedLast(t *testing.T) {
	srcs := []source.Source{
		{Name: "company", Priority: 30},
		{Name: "team", Priority: 20},
		{Name: "personal", Priority: 10},
	}
	source.SortByPriority(srcs)
	if got, want := names(srcs), []string{"personal", "team", "company"}; !equalSlices(got, want) {
		t.Errorf("order = %v, want %v (highest priority last)", got, want)
	}
}

func TestSortByPriority_stableTieBreak(t *testing.T) {
	// Equal priority must preserve incoming (profile) order.
	srcs := []source.Source{
		{Name: "a", Priority: 10},
		{Name: "b", Priority: 10},
		{Name: "c", Priority: 10},
	}
	source.SortByPriority(srcs)
	if got, want := names(srcs), []string{"a", "b", "c"}; !equalSlices(got, want) {
		t.Errorf("order = %v, want %v (stable for equal priority)", got, want)
	}
}

func TestSortByPriority_allZeroPreservesOrder(t *testing.T) {
	// The all-zero default must leave order untouched (backward compatible).
	srcs := []source.Source{
		{Name: "first"}, {Name: "second"}, {Name: "third"},
	}
	source.SortByPriority(srcs)
	if got, want := names(srcs), []string{"first", "second", "third"}; !equalSlices(got, want) {
		t.Errorf("order = %v, want %v (unchanged for all-zero)", got, want)
	}
}

func TestSortByPriority_mixedExplicitAndDefault(t *testing.T) {
	// Default-0 sources are lowest; explicit positive priority sorts after them.
	srcs := []source.Source{
		{Name: "explicit30", Priority: 30},
		{Name: "default0a"},
		{Name: "explicit10", Priority: 10},
		{Name: "default0b"},
	}
	source.SortByPriority(srcs)
	if got, want := names(srcs), []string{"default0a", "default0b", "explicit10", "explicit30"}; !equalSlices(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestEnsureInstructionFile_createsWhenMissing(t *testing.T) {
	root := t.TempDir()
	s := source.Source{Name: "work", Root: root, Structure: source.DefaultStructure()}

	created, err := s.EnsureInstructionFile()
	if err != nil {
		t.Fatalf("EnsureInstructionFile: %v", err)
	}
	if !created {
		t.Fatal("expected created=true on first call")
	}
	data, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading scaffolded file: %v", err)
	}
	if len(data) == 0 {
		t.Error("scaffolded CLAUDE.md is empty; want a header")
	}
}

func TestEnsureInstructionFile_idempotentWhenPresent(t *testing.T) {
	root := t.TempDir()
	existing := "# my own rules\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	s := source.Source{Name: "work", Root: root, Structure: source.DefaultStructure()}

	created, err := s.EnsureInstructionFile()
	if err != nil {
		t.Fatalf("EnsureInstructionFile: %v", err)
	}
	if created {
		t.Error("expected created=false when file already exists")
	}
	data, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if string(data) != existing {
		t.Errorf("existing content was overwritten: got %q, want %q", data, existing)
	}
}

func TestEnsureInstructionFile_noOpForHierarchicalSource(t *testing.T) {
	root := t.TempDir()
	s := source.Source{
		Name:      "work",
		Root:      root,
		Structure: source.Structure{InstructionGlob: "**/*.md"},
	}

	created, err := s.EnsureInstructionFile()
	if err != nil {
		t.Fatalf("EnsureInstructionFile: %v", err)
	}
	if created {
		t.Error("expected no scaffold for a hierarchical (glob) source")
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("CLAUDE.md should not have been created for a glob source")
	}
}
