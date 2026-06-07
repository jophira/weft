package pidlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrLocked is returned by Acquire when another live process holds the lock.
type ErrLocked struct {
	Path      string
	HolderPID int
}

func (e *ErrLocked) Error() string {
	return fmt.Sprintf("weft watcher is already running (PID %d) — use --no-watch to apply once without watching", e.HolderPID)
}

// Lock represents an acquired PID lock file.
type Lock struct {
	path string
}

// Acquire creates or takes over the lock file at path.
// Returns ErrLocked if a live process already holds the lock.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("pidlock: creating dir: %w", err)
	}

	// If the file exists, check whether the recorded PID is still alive.
	if data, err := os.ReadFile(path); err == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && pid > 0 && processAlive(pid) {
			return nil, &ErrLocked{Path: path, HolderPID: pid}
		}
		// Stale lock — the process is gone; overwrite below.
	}

	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, fmt.Errorf("pidlock: writing lock file: %w", err)
	}
	return &Lock{path: path}, nil
}

// Release removes the lock file. Safe to call more than once.
func (l *Lock) Release() {
	_ = os.Remove(l.path)
}

// processAlive reports whether pid refers to a live process on this machine.
// Uses kill(pid, 0) — the standard POSIX liveness probe (cf. Java: no stdlib equivalent,
// would need /proc or ProcessHandle; Go's os.FindProcess always succeeds on Unix).
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 does not send a signal; it only checks whether the process exists
	// and the caller has permission to signal it.
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
