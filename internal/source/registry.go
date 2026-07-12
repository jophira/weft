package source

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jophira/weft/internal/locate"
)

// compile-time check: FileRegistry must satisfy Registry.
var _ Registry = (*FileRegistry)(nil)

// validName enforces lowercase-start, alphanumeric + hyphen/underscore only.
var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// FileRegistry persists each Source as a YAML file under a directory.
type FileRegistry struct {
	dir string // absolute path to ~/.config/weft/sources/
}

func NewFileRegistry(dir string) *FileRegistry {
	return &FileRegistry{dir: locate.ExpandHome(dir)}
}

// Add writes a new source YAML file. Errors if the name already exists.
func (r *FileRegistry) Add(s Source) error {
	if !validName.MatchString(s.Name) {
		return fmt.Errorf(
			"invalid name %q: must start with a letter and contain only lowercase letters, digits, hyphens or underscores",
			s.Name,
		)
	}
	if _, err := os.Stat(r.filePath(s.Name)); err == nil {
		return fmt.Errorf("source %q already exists — use 'weft source remove %s' first", s.Name, s.Name)
	}
	return r.write(s)
}

// Update overwrites an existing source's YAML (e.g. after relocating its root).
// Errors if the source does not already exist — use Add to create.
func (r *FileRegistry) Update(s Source) error {
	if _, err := os.Stat(r.filePath(s.Name)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source %q not found", s.Name)
		}
		return fmt.Errorf("checking source %q: %w", s.Name, err)
	}
	return r.write(s)
}

// write normalises and persists a source YAML, creating the directory as needed.
// Shared by Add (create) and Update (overwrite).
func (r *FileRegistry) write(s Source) error {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return fmt.Errorf("creating sources directory: %w", err)
	}
	// Normalise root to ~/… for portability across machines.
	s.Root = locate.Tilde(s.Root)
	if s.Branch == "" {
		s.Branch = "main"
	}
	if s.Structure.isZero() {
		s.Structure = DefaultStructure()
	}
	data, err := yaml.Marshal(&s)
	if err != nil {
		return fmt.Errorf("serialising source: %w", err)
	}
	return os.WriteFile(r.filePath(s.Name), data, 0o644)
}

// Remove deletes the source YAML file.
func (r *FileRegistry) Remove(name string) error {
	if err := os.Remove(r.filePath(name)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source %q not found", name)
		}
		return fmt.Errorf("removing source %q: %w", name, err)
	}
	return nil
}

// Get reads and parses one source by name.
func (r *FileRegistry) Get(name string) (*Source, error) {
	data, err := os.ReadFile(r.filePath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("source %q not found", name)
		}
		return nil, fmt.Errorf("reading source %q: %w", name, err)
	}
	var s Source
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing source %q: %w", name, err)
	}
	return &s, nil
}

// List returns all registered sources, sorted by filename.
func (r *FileRegistry) List() ([]Source, error) {
	entries, err := os.ReadDir(r.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading sources directory: %w", err)
	}
	var sources []Source
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		s, err := r.Get(name)
		if err != nil {
			return nil, err
		}
		sources = append(sources, *s)
	}
	return sources, nil
}

func (r *FileRegistry) filePath(name string) string {
	return filepath.Join(r.dir, name+".yaml")
}
