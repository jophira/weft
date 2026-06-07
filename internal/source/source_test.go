package source_test

import (
	"strings"
	"testing"

	"github.com/jophira/weft/internal/source"
)

// ── ManagedDirs ───────────────────────────────────────────────────────────────

func TestManagedDirs_defaultStructure(t *testing.T) {
	s := source.DefaultStructure()
	dirs := s.ManagedDirs()
	// Default structure has commands, agents, skills, memory, hooks (5 dirs).
	if len(dirs) != 5 {
		t.Errorf("ManagedDirs() len = %d, want 5; dirs=%v", len(dirs), dirs)
	}
}

func TestManagedDirs_excludesProjects(t *testing.T) {
	s := source.DefaultStructure()
	s.Projects = "projects/"
	dirs := s.ManagedDirs()
	for _, d := range dirs {
		if d == "projects" || d == "projects/" {
			t.Errorf("ManagedDirs() includes Projects dir %q", d)
		}
	}
}

func TestManagedDirs_emptyFields(t *testing.T) {
	s := source.Structure{}
	dirs := s.ManagedDirs()
	if len(dirs) != 0 {
		t.Errorf("ManagedDirs() on empty structure = %v, want empty", dirs)
	}
}

func TestManagedDirs_tripsTrailingSlash(t *testing.T) {
	s := source.Structure{Commands: "commands/", Agents: "  agents/  "}
	dirs := s.ManagedDirs()
	for _, d := range dirs {
		if strings.HasSuffix(d, "/") {
			t.Errorf("ManagedDirs() returned dir with trailing slash: %q", d)
		}
	}
}

func TestManagedDirs_skipsWhitespaceOnly(t *testing.T) {
	s := source.Structure{Commands: "   ", Agents: "agents"}
	dirs := s.ManagedDirs()
	if len(dirs) != 1 || dirs[0] != "agents" {
		t.Errorf("ManagedDirs() = %v, want [agents]", dirs)
	}
}

// ── AllDirs ───────────────────────────────────────────────────────────────────

func TestAllDirs_includesProjectsWhenSet(t *testing.T) {
	s := source.DefaultStructure()
	s.Projects = "projects/"
	dirs := s.AllDirs()
	found := false
	for _, d := range dirs {
		if d == "projects" {
			found = true
		}
	}
	if !found {
		t.Errorf("AllDirs() does not include Projects dir; dirs=%v", dirs)
	}
}

func TestAllDirs_supersetOfManagedDirs(t *testing.T) {
	s := source.DefaultStructure()
	s.Projects = "projects/"
	all := s.AllDirs()
	managed := s.ManagedDirs()
	if len(all) <= len(managed) {
		t.Errorf("AllDirs() len=%d should be > ManagedDirs() len=%d when Projects is set", len(all), len(managed))
	}
}

func TestAllDirs_withoutProjects_equalsManagedDirs(t *testing.T) {
	s := source.DefaultStructure()
	all := s.AllDirs()
	managed := s.ManagedDirs()
	if len(all) != len(managed) {
		t.Errorf("AllDirs() len=%d, ManagedDirs() len=%d; should be equal when Projects is empty", len(all), len(managed))
	}
}
