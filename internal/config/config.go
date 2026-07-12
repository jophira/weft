package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ActiveProfile         string `yaml:"active_profile"            mapstructure:"active_profile"`
	SourcesDir            string `yaml:"sources_dir"               mapstructure:"sources_dir"`
	ProfilesDir           string `yaml:"profiles_dir"              mapstructure:"profiles_dir"`
	HooksDir              string `yaml:"hooks_dir"                 mapstructure:"hooks_dir"`
	WarnInstructionSizeKB int    `yaml:"warn_instruction_size_kb"  mapstructure:"warn_instruction_size_kb"`
}

func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".config", "weft"), nil
}

func Defaults() (*Config, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return &Config{
		ActiveProfile:         "",
		SourcesDir:            filepath.Join(dir, "sources"),
		ProfilesDir:           filepath.Join(dir, "profiles"),
		HooksDir:              filepath.Join(dir, "hooks"),
		WarnInstructionSizeKB: 96,
	}, nil
}

func EnsureDirs(c *Config) error {
	for _, d := range []string{c.SourcesDir, c.ProfilesDir, c.HooksDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating directory %s: %w", d, err)
		}
	}
	return nil
}

// SetWarnInstructionSizeKB persists warn_instruction_size_kb to config.yaml,
// preserving any other keys already in the file.
func SetWarnInstructionSizeKB(kb int) error {
	return setKey("warn_instruction_size_kb", kb)
}

// activeConfigFile, when non-empty, overrides the file that FilePath / setKey /
// ReadActiveProfile operate on. The CLI sets it (via SetActiveConfigFile in
// initConfig) so a custom --config isolates active-profile state too, rather
// than silently falling back to the global ~/.config/weft/config.yaml.
var activeConfigFile string

// SetActiveConfigFile points the active-profile read/write helpers at path.
// An empty path restores the default (~/.config/weft/config.yaml). It exists so
// --config fully isolates state; see initConfig.
func SetActiveConfigFile(path string) {
	activeConfigFile = path
}

// SetActiveProfile persists active_profile to the active config file,
// preserving any other keys already in it.
func SetActiveProfile(name string) error {
	return setKey("active_profile", name)
}

// FilePath returns the absolute path to the active config file — the same file
// SetActiveProfile writes to. Watch this path to observe out-of-process
// active-profile changes (e.g. a second `weft profile use` handing a profile
// off to a running watcher). Honours --config via SetActiveConfigFile.
func FilePath() (string, error) {
	if activeConfigFile != "" {
		return activeConfigFile, nil
	}
	dir, err := DefaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// ReadActiveProfile reads active_profile fresh from config.yaml on disk.
// Unlike the viper-cached value read at process start, this reflects writes
// made by other processes since startup. Returns "" (no error) when the file
// or key is absent.
func ReadActiveProfile() (string, error) {
	cfgPath, err := FilePath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading config.yaml: %w", err)
	}
	raw := map[string]any{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("config.yaml is corrupt: %w", err)
	}
	if v, ok := raw["active_profile"].(string); ok {
		return v, nil
	}
	return "", nil
}

// setKey writes a single key/value pair to the active config file, preserving
// all other keys.
func setKey(key string, value any) error {
	cfgPath, err := FilePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(cfgPath)

	raw := map[string]any{}
	if data, err := os.ReadFile(cfgPath); err == nil {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("config.yaml is corrupt: %w", err)
		}
	}
	raw[key] = value

	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("serialising config: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	return os.WriteFile(cfgPath, out, 0o644)
}
