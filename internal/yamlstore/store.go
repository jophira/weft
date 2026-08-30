// Package yamlstore implements the shared "one YAML file per name" CRUD
// pattern duplicated across profile.FileManager, source.FileRegistry, and
// hook.FileManager: persist a record as a YAML file under a directory, keyed
// by name.
//
// The name is half a file path, so this package owns validating it. Every
// operation that turns a caller-supplied name into a path checks it first:
// callers used to validate only on create, which left Get, Remove and Update
// joining unchecked input onto the store directory (#277).
package yamlstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jophira/weft/internal/privatefile"
)

// ErrNotFound is returned by Get and Remove when no record exists for a name.
var ErrNotFound = errors.New("not found")

// validName enforces lowercase-start, alphanumeric + hyphen/underscore only.
// It is deliberately an allow-list rather than a "reject .. and /" deny-list:
// a name becomes a filename, and enumerating the ways a path can escape a
// directory across three operating systems is a losing game.
var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// ValidName reports whether name is usable as a record name, with an error
// describing the rule when it is not. Exported so callers can reject bad input
// at the edge with their own wording; the store enforces it regardless.
func ValidName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf(
			"invalid name %q: must start with a lowercase letter and contain only lowercase letters, digits, hyphens or underscores",
			name,
		)
	}
	return nil
}

// Store persists records of type T as one YAML file per name under dir.
type Store[T any] struct {
	dir string
}

// New returns a Store rooted at dir. dir is not created until the first Write.
func New[T any](dir string) *Store[T] {
	return &Store[T]{dir: dir}
}

// filePath returns the on-disk path for name. Unexported: it is only safe to
// call on a name ValidName has already accepted, and keeping it inside the
// package is what guarantees that.
func (s *Store[T]) filePath(name string) string {
	return filepath.Join(s.dir, name+".yaml")
}

// Exists reports whether a record named name is already on disk. An invalid
// name is not an error here — no record can exist under one.
func (s *Store[T]) Exists(name string) bool {
	if ValidName(name) != nil {
		return false
	}
	_, err := os.Stat(s.filePath(name))
	return err == nil
}

// Write serialises v as YAML and persists it under name, creating the
// directory as needed. Used for both create and overwrite.
func (s *Store[T]) Write(name string, v T) error {
	if err := ValidName(name); err != nil {
		return err
	}
	data, err := yaml.Marshal(&v)
	if err != nil {
		return fmt.Errorf("serialising %q: %w", name, err)
	}
	return privatefile.Write(s.filePath(name), data)
}

// Get reads and parses one record by name. Returns ErrNotFound if it doesn't exist.
func (s *Store[T]) Get(name string) (*T, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	return s.get(name)
}

// get is the unvalidated read. Only List uses it directly, on names it read
// back out of the store directory rather than took from a caller.
func (s *Store[T]) get(name string) (*T, error) {
	data, err := os.ReadFile(s.filePath(name))
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
	if err := ValidName(name); err != nil {
		return err
	}
	if err := os.Remove(s.filePath(name)); err != nil {
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
		// s.get, not s.Get: the name came from the store's own directory. A file
		// that predates the validation rule should still list rather than break
		// every listing.
		v, err := s.get(name)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}
