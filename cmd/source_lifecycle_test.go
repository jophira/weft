package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

func TestSourceRelocate_movesContentAndRepointsRegistry(t *testing.T) {
	// Under isolation, HOME and weft_home both resolve to base, and the sources
	// dir sits at base/sources.
	base := withIsolatedConfig(t)
	reg, err := newRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	ext := filepath.Join(base, "external", "mysrc")
	writeFileT(t, filepath.Join(ext, "CLAUDE.md"), "rules")
	if err := reg.Add(source.Source{Name: "mysrc", Root: ext}); err != nil {
		t.Fatalf("add: %v", err)
	}

	runCmd(t, sourceRelocateCmd, []string{"mysrc"})

	dst := filepath.Join(base, "sources", "mysrc")
	if got, _ := os.ReadFile(filepath.Join(dst, "CLAUDE.md")); string(got) != "rules" {
		t.Errorf("content not moved to %s: %q", dst, got)
	}
	// Bridge at old path still resolves.
	if got, _ := os.ReadFile(filepath.Join(ext, "CLAUDE.md")); string(got) != "rules" {
		t.Errorf("bridge symlink does not resolve old path")
	}
	// Registry now points at the new root.
	s, err := reg.Get("mysrc")
	if err != nil {
		t.Fatalf("get after relocate: %v", err)
	}
	if locate.ExpandHome(s.Root) != dst {
		t.Errorf("registry root = %q, want %q", locate.ExpandHome(s.Root), dst)
	}

	// Idempotent: second relocate is a no-op.
	out := runCmd(t, sourceRelocateCmd, []string{"mysrc"})
	if !strings.Contains(out, "same path") && !strings.Contains(out, "already migrated") {
		t.Errorf("second relocate not idempotent:\n%s", out)
	}
}

func TestSourceRename_updatesRegistryAndProfiles(t *testing.T) {
	withIsolatedConfig(t)
	reg, _ := newRegistry()
	if err := reg.Add(source.Source{Name: "oldname", Root: t.TempDir()}); err != nil {
		t.Fatalf("add: %v", err)
	}
	pm, _ := newProfileManager()
	if err := pm.Create(profile.Profile{
		Name:      "p1",
		Sources:   []string{"oldname"},
		Overlay:   profile.OverlayCascade,
		WriteBack: profile.WriteBack{Default: "oldname"},
	}); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	out := runCmd(t, sourceRenameCmd, []string{"oldname", "newname"})
	if !strings.Contains(out, "updated profile \"p1\"") {
		t.Errorf("rename did not report profile update:\n%s", out)
	}

	if _, err := reg.Get("oldname"); err == nil {
		t.Error("old source name still registered after rename")
	}
	if _, err := reg.Get("newname"); err != nil {
		t.Errorf("new source name not registered: %v", err)
	}
	p, err := pm.Get("p1")
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if len(p.Sources) != 1 || p.Sources[0] != "newname" {
		t.Errorf("profile sources not rewritten: %v", p.Sources)
	}
	if p.WriteBack.Default != "newname" {
		t.Errorf("write-back default not rewritten: %q", p.WriteBack.Default)
	}
}

func TestSourceRename_refusesExistingTarget(t *testing.T) {
	withIsolatedConfig(t)
	reg, _ := newRegistry()
	_ = reg.Add(source.Source{Name: "a", Root: t.TempDir()})
	_ = reg.Add(source.Source{Name: "b", Root: t.TempDir()})
	holder := newHolderCmd()
	if err := sourceRenameCmd.RunE(holder, []string{"a", "b"}); err == nil {
		t.Fatal("expected error renaming onto an existing source name")
	}
	// 'a' must still exist (no destructive half-rename).
	if _, err := reg.Get("a"); err != nil {
		t.Errorf("source 'a' lost after failed rename: %v", err)
	}
}

func TestReportProfileIntegrity_flagsDanglingReference(t *testing.T) {
	withIsolatedConfig(t)
	reg, _ := newRegistry()
	_ = reg.Add(source.Source{Name: "real", Root: t.TempDir()})
	pm, _ := newProfileManager()
	_ = pm.Create(profile.Profile{Name: "broken", Sources: []string{"ghost"}, Overlay: profile.OverlayCascade})

	var sb strings.Builder
	reportProfileIntegrity(&sb)
	got := sb.String()
	if !strings.Contains(got, `profile "broken" references unregistered source "ghost"`) {
		t.Errorf("integrity check missed dangling reference:\n%s", got)
	}
	if !strings.Contains(got, "weft source rename") {
		t.Errorf("integrity check did not offer the fix:\n%s", got)
	}
}
