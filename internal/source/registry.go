package source

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/yamlstore"
)

// compile-time check: FileRegistry must satisfy Registry.
var _ Registry = (*FileRegistry)(nil)

// validName enforces lowercase-start, alphanumeric + hyphen/underscore only.
var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// FileRegistry persists each Source as a YAML file under a directory.
type FileRegistry struct {
	store *yamlstore.Store[Source]
}

func NewFileRegistry(dir string) *FileRegistry {
	return &FileRegistry{store: yamlstore.New[Source](locate.ExpandHome(dir))}
}

// Add writes a new source YAML file. Errors if the name already exists.
func (r *FileRegistry) Add(s Source) error {
	if !validName.MatchString(s.Name) {
		return fmt.Errorf(
			"invalid name %q: must start with a letter and contain only lowercase letters, digits, hyphens or underscores",
			s.Name,
		)
	}
	if r.store.Exists(s.Name) {
		return fmt.Errorf("source %q already exists — use 'weft source remove %s' first", s.Name, s.Name)
	}
	return r.write(s)
}

// Update overwrites an existing source's YAML (e.g. after relocating its root).
// Errors if the source does not already exist — use Add to create.
func (r *FileRegistry) Update(s Source) error {
	if !r.store.Exists(s.Name) {
		return fmt.Errorf("source %q not found", s.Name)
	}
	return r.write(s)
}

// write normalises a source before persisting it. Shared by Add (create) and
// Update (overwrite).
func (r *FileRegistry) write(s Source) error {
	// Normalise root to ~/… for portability across machines.
	s.Root = locate.Tilde(s.Root)
	if s.Branch == "" {
		s.Branch = "main"
	}
	if s.Structure.isZero() {
		s.Structure = DefaultStructure()
	}
	return r.store.Write(s.Name, s)
}

// Remove deletes the source YAML file.
func (r *FileRegistry) Remove(name string) error {
	if err := r.store.Remove(name); err != nil {
		if errors.Is(err, yamlstore.ErrNotFound) {
			return fmt.Errorf("source %q not found", name)
		}
		return fmt.Errorf("removing source %q: %w", name, err)
	}
	return nil
}

// Get reads and parses one source by name.
func (r *FileRegistry) Get(name string) (*Source, error) {
	s, err := r.store.Get(name)
	if err != nil {
		if errors.Is(err, yamlstore.ErrNotFound) {
			return nil, fmt.Errorf("source %q not found", name)
		}
		return nil, fmt.Errorf("reading source %q: %w", name, err)
	}
	return s, nil
}

// List returns all registered sources, sorted by filename.
func (r *FileRegistry) List() ([]Source, error) {
	return r.store.List()
}
