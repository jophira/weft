package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

// TestInitConfigIsolatesStateDirs verifies that a custom --config file isolates
// weft's state: sources_dir, profiles_dir and hooks_dir default to directories
// beside the config file, not the global ~/.config/weft. This is the regression
// guard for issue #164, where `source add --config <file>` leaked into the
// global sources dir.
func TestInitConfigIsolatesStateDirs(t *testing.T) {
	base := t.TempDir()
	cfg := filepath.Join(base, "config.yaml")
	if err := os.WriteFile(cfg, []byte("active_profile: test\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Arrange global state used by initConfig, restoring it afterwards so we do
	// not disturb other tests that share the package-level viper/cfgFile.
	prevCfgFile, prevBase := cfgFile, cfgBaseDir
	t.Cleanup(func() {
		cfgFile, cfgBaseDir = prevCfgFile, prevBase
		viper.Reset()
	})
	viper.Reset()
	cfgFile = cfg

	initConfig()

	if got, want := configDir(), base; got != want {
		t.Errorf("configDir() = %q, want %q", got, want)
	}
	for _, tc := range []struct{ key, sub string }{
		{"sources_dir", "sources"},
		{"profiles_dir", "profiles"},
		{"hooks_dir", "hooks"},
	} {
		if got, want := viper.GetString(tc.key), filepath.Join(base, tc.sub); got != want {
			t.Errorf("%s = %q, want %q", tc.key, got, want)
		}
	}
}

// TestInitConfigExplicitDirWins verifies that an explicit sources_dir in the
// config file overrides the config-relative default (viper precedence).
func TestInitConfigExplicitDirWins(t *testing.T) {
	base := t.TempDir()
	custom := filepath.Join(base, "elsewhere", "srcs")
	cfg := filepath.Join(base, "config.yaml")
	if err := os.WriteFile(cfg, []byte("sources_dir: "+custom+"\n"), 0o644); err != nil {
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

	if got := viper.GetString("sources_dir"); got != custom {
		t.Errorf("sources_dir = %q, want explicit %q", got, custom)
	}
}
