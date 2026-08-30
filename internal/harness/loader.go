package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/locate"
)

type harnessEntry struct {
	Name         string     `yaml:"name"`
	DetectPath   string     `yaml:"detect_path"`
	DetectBinary stringList `yaml:"detect_binary"`
	ConfigDir    string     `yaml:"config_dir"`
}

// stringList accepts either a scalar or a sequence in YAML, so detect_binary can
// name one binary or several without a second key. Every harnesses.yaml written
// before the field was widened still parses.
//
// cf. Java: a Jackson deserialiser accepting both String and List<String> for
// one property.
type stringList []string

func (s *stringList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var one string
		if err := n.Decode(&one); err != nil {
			return err
		}
		if one == "" {
			*s = nil
			return nil
		}
		*s = stringList{one}
		return nil
	}
	var many []string
	if err := n.Decode(&many); err != nil {
		return err
	}
	*s = many
	return nil
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
//	                                # a list is accepted too: [my-tool, mytool]
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
	seen := make(map[string]struct{}, len(f.Harnesses))
	for i, e := range f.Harnesses {
		if vErr := validateEntry(e, i, seen); vErr != nil {
			return nil, vErr
		}
		seen[e.Name] = struct{}{}
		candidates := entryCandidates(e)
		out = append(out, Known{
			H: &GenericHarness{
				name:           e.Name,
				detectBinaries: e.DetectBinary,
				candidates:     candidates,
			},
			ConfigPath: "", // resolved at runtime via ConfigPather
		})
	}
	return out, nil
}

// validateEntry rejects a harness entry that could project outside the user's
// home. config_dir and detect_path are joined onto $HOME, so an absolute path
// or a parent traversal makes a generic harness write wherever it likes (#279).
//
// A bad entry fails the load rather than being skipped: silently dropping a
// harness the user configured would look like weft simply not supporting it,
// and they would go looking in the wrong place.
func validateEntry(e harnessEntry, i int, seen map[string]struct{}) error {
	where := fmt.Sprintf("harnesses.yaml entry %d", i+1)
	if e.Name == "" {
		return fmt.Errorf("%s: name is required", where)
	}
	where = fmt.Sprintf("harnesses.yaml entry %q", e.Name)
	if _, dup := seen[e.Name]; dup {
		return fmt.Errorf("%s: duplicate name — each harness must be named once", where)
	}
	if e.ConfigDir == "" && e.DetectPath == "" {
		return fmt.Errorf("%s: needs config_dir or detect_path", where)
	}
	for field, val := range map[string]string{"config_dir": e.ConfigDir, "detect_path": e.DetectPath} {
		if val == "" {
			continue // absent is fine; empty-after-set is caught by the pair check above
		}
		if hErr := homeRelative(val); hErr != nil {
			return fmt.Errorf("%s: %s %w", where, field, hErr)
		}
	}
	return nil
}

// homeRelative reports whether p is a clean path that stays under $HOME when
// joined to it.
func homeRelative(p string) error {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return fmt.Errorf("%q must be relative to $HOME, not absolute", p)
	}
	// Windows drive-relative paths ("C:foo") are absolute enough to matter and
	// filepath.IsAbs does not catch them when weft runs on Unix.
	if len(p) >= 2 && p[1] == ':' {
		return fmt.Errorf("%q must be relative to $HOME, not drive-qualified", p)
	}
	// Compare against the cleaned form: "a/../../b" cleans to "../b".
	clean := filepath.Clean(filepath.FromSlash(p))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q escapes $HOME", p)
	}
	if clean == "." {
		return fmt.Errorf("%q resolves to $HOME itself, which weft will not manage wholesale", p)
	}
	return nil
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
