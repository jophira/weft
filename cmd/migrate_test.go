package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/testenv"
)

// withRelocatableConfig sets up a config whose weft_home differs from the
// engine-room base, so `weft migrate` has real content to relocate. HOME is
// isolated so the global-audit lookup never touches the real ~/.weft.
// Returns (base, home) where home == weft_home.
func withRelocatableConfig(t *testing.T) (base, home string) {
	t.Helper()
	base = t.TempDir()
	testenv.SetHome(t, base)
	home = filepath.Join(base, "weft")

	cfg := filepath.Join(base, "config.yaml")
	if err := os.WriteFile(cfg, []byte("weft_home: "+home+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	prevCfgFile, prevBase := cfgFile, cfgBaseDir
	prevDry, prevDocs := migrateDryRun, migrateDocs
	t.Cleanup(func() {
		cfgFile, cfgBaseDir = prevCfgFile, prevBase
		migrateDryRun, migrateDocs = prevDry, prevDocs
		viper.Reset()
	})
	viper.Reset()
	cfgFile = cfg
	migrateDryRun, migrateDocs = false, false
	initConfig()
	return base, home
}

// seedSource registers a source named `name` with content at base/external/name.
func seedSource(t *testing.T, base, name, file, content string) string {
	t.Helper()
	root := filepath.Join(base, "external", name)
	writeFileT(t, filepath.Join(root, file), content)
	reg, err := newRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if err := reg.Add(source.Source{Name: name, Root: root}); err != nil {
		t.Fatalf("add source: %v", err)
	}
	return root
}

func TestMigrate_relocatesRegisteredSourceContent(t *testing.T) {
	base, home := withRelocatableConfig(t)
	root := seedSource(t, base, "team", "CLAUDE.md", "rules")

	runCmd(t, migrateCmd, nil)

	// Content now under the workbench, reachable via the old path's bridge.
	dst := filepath.Join(home, "sources", "team")
	if got, _ := os.ReadFile(filepath.Join(dst, "CLAUDE.md")); string(got) != "rules" {
		t.Errorf("content not relocated to %s", dst)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md")); string(got) != "rules" {
		t.Errorf("bridge symlink at old root does not resolve")
	}
	// Registry entry repointed at the new root.
	reg, _ := newRegistry()
	s, _ := reg.Get("team")
	if filepath.Clean(expandTilde(t, s.Root)) != dst {
		t.Errorf("registry root = %q, want %q", s.Root, dst)
	}
}

func TestMigrate_idempotentSecondRun(t *testing.T) {
	base, _ := withRelocatableConfig(t)
	seedSource(t, base, "team", "a.md", "x")

	runCmd(t, migrateCmd, nil)
	viper.Reset()
	initConfig()
	out := runCmd(t, migrateCmd, nil)
	if !strings.Contains(out, "migration complete") {
		t.Errorf("second migrate did not complete cleanly:\n%s", out)
	}
}

func TestMigrate_dryRunChangesNothing(t *testing.T) {
	base, home := withRelocatableConfig(t)
	migrateDryRun = true
	root := seedSource(t, base, "team", "a.md", "x")

	out := runCmd(t, migrateCmd, nil)
	if !strings.Contains(out, "dry run complete") {
		t.Errorf("expected dry-run summary, got:\n%s", out)
	}
	if dirExists(filepath.Join(home, "sources", "team")) {
		t.Errorf("dry run created the destination")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.md")); string(got) != "x" {
		t.Errorf("dry run disturbed the source")
	}
}

// TestMigrate_configIsolationLeavesGlobalAuditUntouched is the regression guard
// for the isolation bug where `weft migrate --config <file>` reached into the
// real HOME and moved the machine-wide ~/.weft/audit. Under --config, the global
// audit must be left completely alone.
func TestMigrate_configIsolationLeavesGlobalAuditUntouched(t *testing.T) {
	base, _ := withRelocatableConfig(t) // sets HOME = base, cfgFile != ""
	globalAudit := filepath.Join(base, ".weft", "audit")
	roll := filepath.Join(globalAudit, "2026-07.jsonl")
	writeFileT(t, roll, "{}\n")

	runCmd(t, migrateCmd, nil)

	if got, err := os.ReadFile(roll); err != nil || string(got) != "{}\n" {
		t.Errorf("--config migrate disturbed the global audit rollup (err=%v, content=%q)", err, got)
	}
}

func TestDocsAdopt_setsDocsDirWhenNoDocsYet(t *testing.T) {
	base, home := withRelocatableConfig(t)
	runCmd(t, docsAdoptCmd, nil)

	adopted := filepath.Join(home, "docs")
	if !dirExists(adopted) {
		t.Errorf("docs adopt did not create %s", adopted)
	}
	data, _ := os.ReadFile(filepath.Join(base, "config.yaml"))
	if !strings.Contains(string(data), adopted) {
		t.Errorf("docs_dir not persisted:\n%s", data)
	}
}

// expandTilde resolves a possibly ~-prefixed registry root for comparison.
func expandTilde(t *testing.T, p string) string {
	t.Helper()
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
