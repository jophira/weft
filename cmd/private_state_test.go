package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/config"
)

// The engine room holds weft's record of the user's rules, and conflict backups
// hold whole rule files verbatim. After a real apply, nothing under the config
// dir may be readable or searchable by anyone but its owner (#280).
func TestApply_leavesNoWorldReadableStateInTheConfigDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}
	cfgDir := withIsolatedConfig(t)

	srcs := buildLayeredSources(t)
	reg, err := newRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	for _, s := range srcs {
		if addErr := reg.Add(s); addErr != nil {
			t.Fatalf("add source %q: %v", s.Name, addErr)
		}
	}
	p := twoTierBTargets()
	pm, err := newProfileManager()
	if err != nil {
		t.Fatalf("profile manager: %v", err)
	}
	if cErr := pm.Create(*p); cErr != nil {
		t.Fatalf("create profile: %v", cErr)
	}
	activate(t, p.Name)
	// The production write path for config.yaml, so its permissions are the
	// ones weft produces rather than the ones the fixture happened to use.
	if sErr := config.SetActiveProfile(p.Name); sErr != nil {
		t.Fatalf("SetActiveProfile: %v", sErr)
	}
	if aErr := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, false); aErr != nil {
		t.Fatalf("mergeAndApply: %v", aErr)
	}

	// Force a conflict backup: edit a projected sidecar file so the next apply
	// sees it as externally modified and preserves a copy under the config dir.
	prompt := filepath.Join(cfgDir, ".codex", "prompts", "hello.md")
	if err := os.WriteFile(prompt, []byte("hand edited\n"), 0o644); err != nil {
		t.Fatalf("editing the projected file: %v", err)
	}
	if aErr := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, false); aErr != nil {
		t.Fatalf("second mergeAndApply: %v", aErr)
	}

	// The harness homes are projected inside cfgDir by withIsolatedConfig, and
	// those files belong to the user's tools, not to the engine room.
	harnessHomes := []string{".codex", ".codeium", ".claude", ".cursor", ".gemini", ".config"}
	isHarnessHome := func(rel string) bool {
		top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		for _, h := range harnessHomes {
			if top == h {
				return true
			}
		}
		return false
	}

	checked := 0
	walkErr := filepath.WalkDir(cfgDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rErr := filepath.Rel(cfgDir, path)
		if rErr != nil || rel == "." {
			return rErr
		}
		if isHarnessHome(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, iErr := d.Info()
		if iErr != nil {
			return iErr
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s is group/world accessible (%o)", rel, perm)
		}
		checked++
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking the config dir: %v", walkErr)
	}
	if checked == 0 {
		t.Fatal("walked no engine-room files — the test proves nothing")
	}

	// The backup is the file the report singles out, so assert it was actually
	// produced rather than trusting the walk to have covered one.
	backups := filepath.Join(cfgDir, "backups")
	found := false
	_ = filepath.WalkDir(backups, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			found = true
		}
		return nil
	})
	if !found {
		t.Error("no conflict backup was written, so its permissions went unchecked")
	}
}
