package pidlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrLocked is returned by Acquire when another live process already holds the lock.
// With flock(2) there is no way to retrieve the holder's PID from user-space, so the
// sentinel is a plain error value rather than a struct — callers check with errors.Is.
var ErrLocked = errors.New("weft watcher is already running — use --no-watch to apply once without watching")

// Lock represents an acquired flock(2) exclusive lock on a file.
// The lock is held for exactly as long as the underlying *os.File remains open;
// the kernel releases it automatically when the process exits (including crashes
// and SIGKILL), so there is no stale-lock problem and no PID tracking is needed.
// (cf. Java: no stdlib equivalent — would need FileLock from java.nio.channels)
type Lock struct {
	f *os.File
}

// Acquire opens (or creates) the lock file at path and acquires an exclusive
// non-blocking flock(2) on it.
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

	// LOCK_EX = exclusive; LOCK_NB = non-blocking (returns EWOULDBLOCK immediately
	// if the lock is already held, rather than blocking until released).
	// cf. Java: FileLock.tryLock() is the non-blocking equivalent.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		// EWOULDBLOCK (aliased to EAGAIN on Linux) means another process holds it.
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("pidlock: flock: %w", err)
	}

	return &Lock{f: f}, nil
}

// Release closes the lock file, which implicitly releases the flock(2) lock.
// Safe to call more than once (subsequent calls return an error but do not panic).
func (l *Lock) Release() error {
	return l.f.Close()
}
