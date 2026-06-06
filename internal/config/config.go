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

// SetActiveProfile persists active_profile to ~/.config/weft/config.yaml,
// preserving any other keys already in the file.
func SetActiveProfile(name string) error {
	return setKey("active_profile", name)
}

// setKey writes a single key/value pair to config.yaml, preserving all other keys.
func setKey(key string, value any) error {
	dir, err := DefaultDir()
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(dir, "config.yaml")

	raw := map[string]any{}
	if data, err := os.ReadFile(cfgPath); err == nil {
		_ = yaml.Unmarshal(data, &raw)
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
