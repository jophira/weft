//go:build windows

package pidlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// ErrLocked is returned by Acquire when another live process already holds the lock.
var ErrLocked = errors.New("weft watcher is already running — use --no-watch to apply once without watching")

// Lock represents an acquired exclusive lock on a file.
// The lock is released when the underlying *os.File is closed.
// (cf. Unix: flock(2) — Windows uses LockFileEx from kernel32)
type Lock struct {
	f *os.File
}

// Acquire opens (or creates) the lock file at path and acquires an exclusive
// non-blocking lock via LockFileEx.
//
// Returns ErrLocked when another process holds the lock; any other error
// indicates an unexpected OS failure.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("pidlock: creating dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("pidlock: opening lock file: %w", err)
	}

	// LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY is the Windows equivalent
	// of flock(LOCK_EX|LOCK_NB) — exclusive, non-blocking.
	// cf. Unix: syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol,
	)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("pidlock: LockFileEx: %w", err)
	}

	return &Lock{f: f}, nil
}

// Release closes the lock file, which implicitly releases the Windows file lock.
// Safe to call more than once (subsequent calls return an error but do not panic).
func (l *Lock) Release() error {
	return l.f.Close()
}
