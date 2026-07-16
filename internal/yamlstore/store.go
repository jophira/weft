// Package yamlstore implements the shared "one YAML file per name" CRUD
// pattern duplicated across profile.FileManager, source.FileRegistry, and
// hook.FileManager: persist a record as a YAML file under a directory, keyed
// by name. Domain-specific validation stays in each caller; this package only
// owns the file mechanics.
package yamlstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrNotFound is returned by Get and Remove when no record exists for a name.
var ErrNotFound = errors.New("not found")

// Store persists records of type T as one YAML file per name under dir.
type Store[T any] struct {
	dir string
}

// New returns a Store rooted at dir. dir is not created until the first Write.
func New[T any](dir string) *Store[T] {
	return &Store[T]{dir: dir}
}

// FilePath returns the on-disk path for name, without checking it exists.
func (s *Store[T]) FilePath(name string) string {
	return filepath.Join(s.dir, name+".yaml")
}

// Exists reports whether a record named name is already on disk.
func (s *Store[T]) Exists(name string) bool {
	_, err := os.Stat(s.FilePath(name))
	return err == nil
}

// Write serialises v as YAML and persists it under name, creating the
// directory as needed. Used for both create and overwrite.
func (s *Store[T]) Write(name string, v T) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	data, err := yaml.Marshal(&v)
	if err != nil {
		return fmt.Errorf("serialising %q: %w", name, err)
	}
	return os.WriteFile(s.FilePath(name), data, 0o644)
}

// Get reads and parses one record by name. Returns ErrNotFound if it doesn't exist.
func (s *Store[T]) Get(name string) (*T, error) {
	data, err := os.ReadFile(s.FilePath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("reading %q: %w", name, err)
	}
	var v T
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parsing %q: %w", name, err)
	}
	return &v, nil
}

// Remove deletes the YAML file for name. Returns ErrNotFound if it doesn't exist.
func (s *Store[T]) Remove(name string) error {
	if err := os.Remove(s.FilePath(name)); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("removing %q: %w", name, err)
	}
	return nil
}

// List returns every record in dir, sorted by filename. A missing directory
// is treated as an empty store rather than an error.
func (s *Store[T]) List() ([]T, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}
	var out []T
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		v, err := s.Get(name)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}
