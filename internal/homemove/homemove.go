// Package homemove relocates a weft directory to its ADR-0003 home without data
// loss. It is the mechanical core of `weft migrate` / `weft docs adopt`:
// idempotent, refuses to clobber a populated destination, and can leave a
// symlink bridge at the old path so pre-existing absolute references keep
// resolving.
//
// cf. Java: think of Move as an idempotent Files.move(src, dst,
// REPLACE_EXISTING?) — except it deliberately does NOT replace a non-empty
// destination, and it falls back to copy+delete across filesystem boundaries
// (where os.Rename would fail with EXDEV).
package homemove

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Result reports what Move did, for human-readable migration output.
type Result struct {
	Moved      bool   // content was relocated src -> dst
	Bridged    bool   // a symlink was left at src pointing to dst
	SkipReason string // non-empty when Move was a no-op (already done / nothing to do)
}

// ErrDestPopulated is returned when dst already exists with content and differs
// from src, so moving would clobber authored work. The caller should surface it
// and leave both paths untouched.
var ErrDestPopulated = errors.New("destination already exists and is not empty")

// Move relocates src to dst. It is idempotent and non-destructive:
//
//   - src absent, or src already a symlink to dst -> no-op (already migrated).
//   - src == dst -> no-op.
//   - dst exists and is non-empty (and not the same inode as src) -> ErrDestPopulated.
//   - otherwise: rename src -> dst (copy+delete fallback across filesystems),
//     then, if bridge, best-effort symlink src -> dst.
//
// A failed bridge symlink (e.g. unprivileged Windows) is not fatal: the move
// succeeded, so Result.Moved is true and the symlink error is returned for the
// caller to warn about.
func Move(src, dst string, bridge bool) (Result, error) {
	if src == dst {
		return Result{SkipReason: "source and destination are the same path"}, nil
	}

	srcInfo, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return Result{SkipReason: "nothing to migrate (source absent)"}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("inspecting %s: %w", src, err)
	}

	// Already migrated on a previous run: src is a symlink pointing at dst.
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(src); err == nil && target == dst {
			return Result{SkipReason: "already migrated (bridge symlink present)"}, nil
		}
	}

	if populated, err := existsNonEmpty(dst); err != nil {
		return Result{}, err
	} else if populated {
		return Result{}, fmt.Errorf("%s -> %s: %w", src, dst, ErrDestPopulated)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Result{}, fmt.Errorf("creating parent of %s: %w", dst, err)
	}
	// Remove an empty dst so rename has a clean target.
	_ = os.Remove(dst)

	if err := os.Rename(src, dst); err != nil {
		// Cross-filesystem rename fails with EXDEV — fall back to copy+delete.
		if !errors.Is(err, os.ErrExist) {
			if cErr := copyTree(src, dst); cErr != nil {
				return Result{}, fmt.Errorf("moving %s -> %s: %w", src, dst, cErr)
			}
			if rErr := os.RemoveAll(src); rErr != nil {
				return Result{Moved: true}, fmt.Errorf("removing source after copy %s: %w", src, rErr)
			}
		} else {
			return Result{}, fmt.Errorf("moving %s -> %s: %w", src, dst, err)
		}
	}

	res := Result{Moved: true}
	if bridge {
		if err := os.Symlink(dst, src); err != nil {
			return res, fmt.Errorf("moved, but could not create bridge symlink %s -> %s: %w", src, dst, err)
		}
		res.Bridged = true
	}
	return res, nil
}

// existsNonEmpty reports whether path exists and (for directories) holds at
// least one entry. A missing path is not populated.
func existsNonEmpty(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspecting %s: %w", path, err)
	}
	if !info.IsDir() {
		return true, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	return len(entries) > 0, nil
}

// copyTree recursively copies src to dst, preserving file modes. Used only as
// the cross-filesystem fallback for Move.
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.IsDir():
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	default:
		return copyFile(src, dst, info.Mode().Perm())
	}
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // migrating weft-owned paths chosen by the user
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec // ditto
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
