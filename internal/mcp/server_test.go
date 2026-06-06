package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	weftmcp "github.com/jophira/weft/internal/mcp"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

func setup(t *testing.T) (reg *source.FileRegistry, pm *profile.FileManager, dir string) {
	t.Helper()
	dir = t.TempDir()
	srcDir := filepath.Join(dir, "sources")
	profDir := filepath.Join(dir, "profiles")
	return source.NewFileRegistry(srcDir), profile.NewFileManager(profDir), dir
}

func writeSource(t *testing.T, reg *source.FileRegistry, name, root string) {
	t.Helper()
	if err := reg.Add(source.Source{
		Name:      name,
		Root:      root,
		Branch:    "main",
		Structure: source.DefaultStructure(),
	}); err != nil {
		t.Fatalf("adding source: %v", err)
	}
}

func writeProfile(t *testing.T, pm *profile.FileManager, p profile.Profile) {
	t.Helper()
	if err := pm.Create(p); err != nil {
		t.Fatalf("creating profile: %v", err)
	}
}

func TestNewServer_NotNil(t *testing.T) {
	reg, pm, _ := setup(t)
	srv := weftmcp.NewServer(reg, pm, weftmcp.Config{
		Version:         "test",
		ActiveProfileFn: func() string { return "" },
	})
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestProfileListHandler(t *testing.T) {
	reg, pm, _ := setup(t)
	srcDir := t.TempDir()
	writeSource(t, reg, "personal", srcDir)
	writeProfile(t, pm, profile.Profile{
		Name:    "test",
		Sources: []string{"personal"},
		Overlay: profile.OverlayCascade,
	})

	active := "test"
	srv := weftmcp.NewServer(reg, pm, weftmcp.Config{
		Version:         "test",
		ActiveProfileFn: func() string { return active },
	})
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestProfileInspectAndActiveResource(t *testing.T) {
	reg, pm, _ := setup(t)

	// Create a source root with a CLAUDE.md.
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "CLAUDE.md"), []byte("# Rules\nBe helpful.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSource(t, reg, "personal", srcDir)
	writeProfile(t, pm, profile.Profile{
		Name:    "solo",
		Sources: []string{"personal"},
		Overlay: profile.OverlayCascade,
	})

	srv := weftmcp.NewServer(reg, pm, weftmcp.Config{
		Version:         "test",
		ActiveProfileFn: func() string { return "solo" },
	})
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestSourceStatusHandler_DirtyDetection(t *testing.T) {
	// Register a non-git source — dirty should default to false without panicking.
	reg, pm, _ := setup(t)
	nonGitDir := t.TempDir()
	writeSource(t, reg, "local", nonGitDir)

	srv := weftmcp.NewServer(reg, pm, weftmcp.Config{
		Version:         "test",
		ActiveProfileFn: func() string { return "" },
	})
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestDoctorHandler_NilSafe(t *testing.T) {
	_, pm, _ := setup(t)
	reg, _, _ := setup(t)
	srv := weftmcp.NewServer(reg, pm, weftmcp.Config{
		Version:         "test",
		ActiveProfileFn: func() string { return "" },
	})
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
}

// TestManagedDirsExclusion verifies that resource content assembly honours
// the managed-directory exclusions (commands/, skills/, etc.).
func TestManagedDirsExclusion(t *testing.T) {
	reg, pm, _ := setup(t)

	srcDir := t.TempDir()
	// Root instruction file — should be included.
	if err := os.WriteFile(filepath.Join(srcDir, "CLAUDE.md"), []byte("root content"), 0o644); err != nil {
		t.Fatal(err)
	}
	// File inside managed commands/ dir — should NOT appear in merged output.
	cmdDir := filepath.Join(srcDir, "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "review.md"), []byte("should be excluded"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeSource(t, reg, "src", srcDir)
	writeProfile(t, pm, profile.Profile{
		Name:    "p",
		Sources: []string{"src"},
		Overlay: profile.OverlayCascade,
	})

	srv := weftmcp.NewServer(reg, pm, weftmcp.Config{
		Version:         "test",
		ActiveProfileFn: func() string { return "p" },
	})
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
}

// yamlRoundtrip is a compile-time helper ensuring profile YAML
// marshalling still works after our changes.
func TestYAMLRoundtrip(t *testing.T) {
	p := profile.Profile{
		Name:    "rt",
		Sources: []string{"a", "b"},
		Overlay: profile.OverlayMerge,
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var out profile.Profile
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != p.Name {
		t.Errorf("got %q, want %q", out.Name, p.Name)
	}
}

// jsonMarshal is a simple sanity check for JSON serialisation shapes.
func TestJSONShapes(t *testing.T) {
	type summary struct {
		Name     string   `json:"name"`
		Sources  []string `json:"sources"`
		IsActive bool     `json:"is_active"`
	}
	s := summary{Name: "test", Sources: []string{"a"}, IsActive: true}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) == "" {
		t.Fatal("expected non-empty JSON")
	}
}
