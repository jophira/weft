package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// compile-time check: FileManager must satisfy Manager.
var _ Manager = (*FileManager)(nil)

var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// FileManager persists each Profile as a YAML file under a directory.
type FileManager struct {
	dir string // absolute path to ~/.config/weft/profiles/
}

func NewFileManager(dir string) *FileManager {
	return &FileManager{dir: expandHome(dir)}
}

// Create writes a new profile YAML file. Errors if the name already exists.
func (m *FileManager) Create(p Profile) error {
	if !validName.MatchString(p.Name) {
		return fmt.Errorf(
			"invalid name %q: must start with a letter and contain only lowercase letters, digits, hyphens or underscores",
			p.Name,
		)
	}
	if len(p.Sources) == 0 {
		return fmt.Errorf("profile must reference at least one source (use --sources)")
	}
	if p.Overlay == "" {
		p.Overlay = OverlayCascade
	}
	if !isValidOverlay(p.Overlay) {
		return fmt.Errorf("invalid overlay %q: must be one of cascade, merge, last-wins", p.Overlay)
	}
	if err := validateWriteBack(p.WriteBack, p.Sources); err != nil {
		return err
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("creating profiles directory: %w", err)
	}
	p2 := m.filePath(p.Name)
	if _, err := os.Stat(p2); err == nil {
		return fmt.Errorf("profile %q already exists — use 'weft profile delete %s' first", p.Name, p.Name)
	}
	data, err := yaml.Marshal(&p)
	if err != nil {
		return fmt.Errorf("serialising profile: %w", err)
	}
	return os.WriteFile(p2, data, 0o644)
}

// Delete removes the profile YAML file.
func (m *FileManager) Delete(name string) error {
	if err := os.Remove(m.filePath(name)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("profile %q not found", name)
		}
		return fmt.Errorf("deleting profile %q: %w", name, err)
	}
	return nil
}

// Get reads and parses one profile by name.
func (m *FileManager) Get(name string) (*Profile, error) {
	data, err := os.ReadFile(m.filePath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("profile %q not found", name)
		}
		return nil, fmt.Errorf("reading profile %q: %w", name, err)
	}
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing profile %q: %w", name, err)
	}
	return &p, nil
}

// List returns all profiles sorted by filename.
func (m *FileManager) List() ([]Profile, error) {
	entries, err := os.ReadDir(m.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading profiles directory: %w", err)
	}
	var profiles []Profile
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		p, err := m.Get(name)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, *p)
	}
	return profiles, nil
}

// Active and Activate are implemented in the 'profile use' feature.
func (m *FileManager) Active() (*Profile, error) {
	return nil, nil
}

func (m *FileManager) Activate(name string) error {
	return fmt.Errorf("not yet implemented — use 'weft profile use'")
}

func (m *FileManager) filePath(name string) string {
	return filepath.Join(m.dir, name+".yaml")
}

func validateWriteBack(wb WriteBack, sources []string) error {
	srcSet := make(map[string]bool, len(sources))
	for _, s := range sources {
		srcSet[s] = true
	}
	if wb.Default != "" && !srcSet[wb.Default] {
		return fmt.Errorf("write_back.default %q is not in the profile's sources", wb.Default)
	}
	for file, src := range wb.Overrides {
		if !srcSet[src] {
			return fmt.Errorf("write_back.overrides[%q] = %q is not in the profile's sources", file, src)
		}
	}
	return nil
}

func isValidOverlay(o Overlay) bool {
	switch o {
	case OverlayCascade, OverlayMerge, OverlayLastWins:
		return true
	}
	return false
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
