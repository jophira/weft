package harness

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/locate"
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
		candidates := entryCandidates(e)
		out = append(out, Known{
			H: &GenericHarness{
				name:         e.Name,
				detectBinary: e.DetectBinary,
				candidates:   candidates,
			},
			ConfigPath: "", // resolved at runtime via ConfigPather
		})
	}
	return out, nil
}

// entryCandidates converts the string fields from a harness YAML entry into
// locate.Candidates. config_dir is the write target and is always included;
// detect_path is added as an additional probe when it differs.
func entryCandidates(e harnessEntry) []locate.Candidate {
	var candidates []locate.Candidate
	if e.ConfigDir != "" {
		configDir := e.ConfigDir
		candidates = append(candidates, locate.Candidate{
			Path: func(home, _ string) string { return filepath.Join(home, configDir) },
		})
	}
	if e.DetectPath != "" && e.DetectPath != e.ConfigDir {
		detectPath := e.DetectPath
		candidates = append(candidates, locate.Candidate{
			Path: func(home, _ string) string { return filepath.Join(home, detectPath) },
		})
	}
	return candidates
}
