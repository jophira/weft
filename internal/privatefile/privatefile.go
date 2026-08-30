// Package privatefile writes weft's own engine-room state: the config file,
// harness manifests, conflict backups, the audit log and the caches under
// ~/.config/weft.
//
// Two properties, both of which the plain os.WriteFile calls this replaced
// lacked (#280).
//
// Owner-only. This state is not the user's rules as their tools read them — it
// is weft's record of them, and a conflict backup holds whole rule files
// verbatim. World-readable files under world-searchable directories put that on
// display to every account on the machine, for no benefit: nothing but weft
// reads any of it.
//
// Atomic. Every write goes to a temp file in the destination directory, is
// flushed to disk, and is renamed into place. A manifest half-written when the
// machine lost power is worse than no manifest: weft would read it back as its
// own record of what it projected, and act on the truncated half.
package privatefile

import (
	"fmt"
	"os"
	"path/filepath"
)

// FileMode and DirMode are the permissions engine-room state is written with:
// readable and writable by its owner, invisible to everyone else.
const (
	FileMode os.FileMode = 0o600
	DirMode  os.FileMode = 0o700
)

// Write creates or replaces path with data, owner-only and atomically.
func Write(path string, data []byte) error {
	return WriteMode(path, data, FileMode)
}

// WriteMode is Write with an explicit mode, for the few files that want
// something other than 0600 — the staged instruction copies are deliberately
// read-only so an editor warns before overwriting a generated file (#261).
// The mode should still be owner-only; nothing here needs a group bit.
func WriteMode(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := MkdirAll(dir); err != nil {
		return err
	}

	// os.CreateTemp already creates with 0600, so the file is never briefly
	// world-readable between creation and chmod.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()

	// One cleanup path for every failure after creation: the temp file must
	// never be left behind, least of all holding the content that failed.
	fail := func(format string, a ...any) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf(format, a...)
	}

	if _, err := tmp.Write(data); err != nil {
		return fail("writing %s: %w", path, err)
	}
	// Rename is atomic, but only orders the directory entry. Without the sync
	// the rename can land while the bytes are still in the page cache, which on
	// a crash leaves a correctly named file full of zeroes.
	if err := tmp.Sync(); err != nil {
		return fail("syncing %s: %w", path, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail("setting permissions on %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("closing %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("renaming into %s: %w", path, err)
	}
	return nil
}

// MkdirAll creates dir and any missing parents owner-only, and tightens dir
// itself if it already exists with group or world bits set.
//
// The tightening is what reaches existing installs. Directories weft created
// before this were 0755, and os.MkdirAll leaves an existing directory's mode
// alone, so without it every machine already running weft would keep its
// world-searchable state directories forever.
func MkdirAll(dir string) error {
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("checking permissions on %s: %w", dir, err)
	}
	if info.Mode().Perm()&^DirMode != 0 {
		if err := os.Chmod(dir, DirMode); err != nil {
			return fmt.Errorf("tightening permissions on %s: %w", dir, err)
		}
	}
	return nil
}
