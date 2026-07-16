package profile

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/yamlstore"
)

// compile-time check: FileManager must satisfy Manager.
var _ Manager = (*FileManager)(nil)

var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// FileManager persists each Profile as a YAML file under a directory.
type FileManager struct {
	store *yamlstore.Store[Profile]
}

func NewFileManager(dir string) *FileManager {
	return &FileManager{store: yamlstore.New[Profile](expandHome(dir))}
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
	if m.store.Exists(p.Name) {
		return fmt.Errorf("profile %q already exists — use 'weft profile delete %s' first", p.Name, p.Name)
	}
	return m.store.Write(p.Name, p)
}

// Update overwrites an existing profile's YAML (e.g. after a source it
// references is renamed). Errors if the profile does not exist — use Create.
func (m *FileManager) Update(p Profile) error {
	if !m.store.Exists(p.Name) {
		return fmt.Errorf("profile %q not found", p.Name)
	}
	return m.store.Write(p.Name, p)
}

// Delete removes the profile YAML file.
func (m *FileManager) Delete(name string) error {
	if err := m.store.Remove(name); err != nil {
		if errors.Is(err, yamlstore.ErrNotFound) {
			return fmt.Errorf("profile %q not found", name)
		}
		return fmt.Errorf("deleting profile %q: %w", name, err)
	}
	return nil
}

// Get reads and parses one profile by name.
func (m *FileManager) Get(name string) (*Profile, error) {
	p, err := m.store.Get(name)
	if err != nil {
		if errors.Is(err, yamlstore.ErrNotFound) {
			return nil, fmt.Errorf("profile %q not found", name)
		}
		return nil, fmt.Errorf("reading profile %q: %w", name, err)
	}
	return p, nil
}

// List returns all profiles sorted by filename.
func (m *FileManager) List() ([]Profile, error) {
	return m.store.List()
}

// Active and Activate are implemented in the 'profile use' feature.
func (m *FileManager) Active() (*Profile, error) {
	return nil, nil
}

func (m *FileManager) Activate(name string) error {
	return fmt.Errorf("not yet implemented — use 'weft profile use'")
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

// expandHome replaces a leading ~/ with the user's absolute home directory.
// Thin wrapper around locate.ExpandHome so path expansion has a single source
// of truth (e.g. cross-OS prefix handling) rather than a divergent copy.
func expandHome(path string) string {
	return locate.ExpandHome(path)
}
