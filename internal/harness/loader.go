package harness

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/jophira/weft/internal/config"
)

type harnessEntry struct {
	Name         string `yaml:"name"`
	DetectPath   string `yaml:"detect_path"`
	DetectBinary string `yaml:"detect_binary"`
	ConfigDir    string `yaml:"config_dir"`
}

type harnessesFile struct {
	Harnesses []harnessEntry `yaml:"harnesses"`
}

// loadConfigHarnesses reads user-defined harnesses from
// ~/.config/weft/harnesses.yaml. A missing file is silently ignored.
//
// Example harnesses.yaml:
//
//	harnesses:
//	  - name: my-tool
//	    detect_path: .my-tool       # relative to $HOME
//	    config_dir: .my-tool        # relative to $HOME
//	    detect_binary: my-tool      # optional: also check PATH
func loadConfigHarnesses() ([]Known, error) {
	dir, err := config.DefaultDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "harnesses.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f harnessesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	out := make([]Known, 0, len(f.Harnesses))
	for _, e := range f.Harnesses {
		out = append(out, Known{
			H: &GenericHarness{
				name:         e.Name,
				detectPath:   e.DetectPath,
				detectBinary: e.DetectBinary,
				configDir:    e.ConfigDir,
			},
			ConfigPath: "~/" + e.ConfigDir,
		})
	}
	return out, nil
}
