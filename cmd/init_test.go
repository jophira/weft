package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/testenv"
)

// withIsolatedConfig points weft at a temp --config base so every managed dir
// (workbench + engine room) resolves under it, and restores the package-level
// viper/cfgFile afterwards. Returns the base dir.
func withIsolatedConfig(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	testenv.SetHome(t, base) // keep any home-relative lookups off the real HOME
	cfg := filepath.Join(base, "config.yaml")
	if err := os.WriteFile(cfg, []byte("active_profile: test\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	prevCfgFile, prevBase := cfgFile, cfgBaseDir
	t.Cleanup(func() {
		cfgFile, cfgBaseDir = prevCfgFile, prevBase
		viper.Reset()
	})
	viper.Reset()
	cfgFile = cfg
	initConfig()
	return base
}

func runCmd(t *testing.T, c *cobra.Command, args []string) string {
	t.Helper()
	buf := &bytes.Buffer{}
	holder := &cobra.Command{}
	holder.SetOut(buf)
	if err := c.RunE(holder, args); err != nil {
		t.Fatalf("%s: %v", c.Name(), err)
	}
	return buf.String()
}

func TestInit_scaffoldsAndIsIdempotent(t *testing.T) {
	base := withIsolatedConfig(t)

	runCmd(t, initCmd, nil)

	// Under --config isolation, weft_home == base, so the whole layout lands here.
	wantDirs := []string{
		filepath.Join(base, "sources"),
		filepath.Join(base, "profiles"),
		filepath.Join(base, "templates"),
		filepath.Join(base, "work", "projects"),
		filepath.Join(base, "work", "tickets"),
		filepath.Join(base, "work", "plans"),
		filepath.Join(base, "work", "inbox"),
		filepath.Join(base, "hooks"),
		filepath.Join(base, "audit"),
	}
	for _, d := range wantDirs {
		if !dirExists(d) {
			t.Errorf("init did not create %s", d)
		}
	}
	if !fileExists(filepath.Join(base, "README.md")) {
		t.Errorf("init did not write home README")
	}

	// Second run must be a clean no-op (idempotency).
	out := runCmd(t, initCmd, nil)
	if !bytes.Contains([]byte(out), []byte("already scaffolded")) {
		t.Errorf("second init not idempotent, output:\n%s", out)
	}
}

func TestInit_doesNotOverwriteExistingReadme(t *testing.T) {
	base := withIsolatedConfig(t)
	readme := filepath.Join(base, "README.md")
	if err := os.WriteFile(readme, []byte("MY OWN NOTES"), 0o644); err != nil {
		t.Fatalf("seed readme: %v", err)
	}
	runCmd(t, initCmd, nil)
	got, _ := os.ReadFile(readme)
	if string(got) != "MY OWN NOTES" {
		t.Errorf("init overwrote existing README: %q", got)
	}
}
