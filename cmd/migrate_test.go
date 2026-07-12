package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/testenv"
)

// withRelocatableConfig sets up a config where weft_home and the sources/profiles
// locations differ, so `weft migrate` has real work to do. Returns (base, home).
func withRelocatableConfig(t *testing.T) (base, home string) {
	t.Helper()
	base = t.TempDir()
	testenv.SetHome(t, base) // isolate legacyGlobalAuditDir()'s ~/.weft lookup
	home = filepath.Join(base, "weft")
	legacySources := filepath.Join(base, "legacy", "sources")
	legacyProfiles := filepath.Join(base, "legacy", "profiles")

	cfg := filepath.Join(base, "config.yaml")
	body := "weft_home: " + home + "\n" +
		"sources_dir: " + legacySources + "\n" +
		"profiles_dir: " + legacyProfiles + "\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
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

func TestMigrate_movesSourcesAndBridges(t *testing.T) {
	base, home := withRelocatableConfig(t)
	legacySources := filepath.Join(base, "legacy", "sources")
	if err := os.MkdirAll(legacySources, 0o755); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacySources, "team.md"), []byte("rules"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	runCmd(t, migrateCmd, nil)

	// Content is now under the workbench, reachable via the old path's bridge.
	newPath := filepath.Join(home, "sources", "team.md")
	if got, _ := os.ReadFile(newPath); string(got) != "rules" {
		t.Errorf("sources not moved to %s: %q", newPath, got)
	}
	if got, _ := os.ReadFile(filepath.Join(legacySources, "team.md")); string(got) != "rules" {
		t.Errorf("bridge symlink at old sources path does not resolve")
	}

	// Config was repointed at the new location.
	data, _ := os.ReadFile(filepath.Join(base, "config.yaml"))
	if !strings.Contains(string(data), filepath.Join(home, "sources")) {
		t.Errorf("config sources_dir not repointed:\n%s", data)
	}
}

func TestMigrate_idempotentSecondRun(t *testing.T) {
	base, _ := withRelocatableConfig(t)
	legacySources := filepath.Join(base, "legacy", "sources")
	if err := os.MkdirAll(legacySources, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacySources, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	runCmd(t, migrateCmd, nil)
	// Re-init so viper reflects the repointed config, then migrate again.
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
	legacySources := filepath.Join(base, "legacy", "sources")
	if err := os.MkdirAll(legacySources, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacySources, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := runCmd(t, migrateCmd, nil)
	if !strings.Contains(out, "dry run complete") {
		t.Errorf("expected dry-run summary, got:\n%s", out)
	}
	if dirExists(filepath.Join(home, "sources")) {
		t.Errorf("dry run created the destination")
	}
	if got, _ := os.ReadFile(filepath.Join(legacySources, "a.md")); string(got) != "x" {
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
	if err := os.MkdirAll(globalAudit, 0o755); err != nil {
		t.Fatalf("seed global audit: %v", err)
	}
	roll := filepath.Join(globalAudit, "2026-07.jsonl")
	if err := os.WriteFile(roll, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("seed rollup: %v", err)
	}

	runCmd(t, migrateCmd, nil)

	if got, err := os.ReadFile(roll); err != nil || string(got) != "{}\n" {
		t.Errorf("--config migrate disturbed the global audit rollup (err=%v, content=%q)", err, got)
	}
}

func TestDocsAdopt_setsDocsDirWhenNoDocsYet(t *testing.T) {
	base, home := withRelocatableConfig(t)
	// docsDir() defaults to $HOME/docs (= base/docs), which does not exist here.
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
