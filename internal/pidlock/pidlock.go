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

// Acquire atomically creates the lock file at path and writes the current PID.
// It uses O_CREATE|O_EXCL so that exactly one caller succeeds when multiple
// processes race — no TOCTOU window between reading and writing.
//
// If the file already exists the existing PID is checked:
//   - alive  → return ErrLocked
//   - stale  → remove the file and retry from the top
//
// Returns ErrLocked if a live process already holds the lock.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("pidlock: creating dir: %w", err)
	}

	for {
		// Attempt atomic creation — only one concurrent caller can win this race.
		// (cf. Java: FileChannel with CREATE_NEW StandardOpenOption)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			// We exclusively created the file; write our PID and we're done.
			_, werr := fmt.Fprintf(f, "%d", os.Getpid())
			cerr := f.Close()
			if werr != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("pidlock: writing lock file: %w", werr)
			}
			if cerr != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("pidlock: closing lock file: %w", cerr)
			}
			return &Lock{path: path}, nil
		}

		// Any error other than "file already exists" is unexpected.
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("pidlock: opening lock file: %w", err)
		}

		// File exists — read the holding PID and check liveness.
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			// File may have been removed between our O_EXCL attempt and ReadFile
			// (another process cleaned up a stale lock). Retry.
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("pidlock: reading lock file: %w", readErr)
		}

		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && pid > 0 && processAlive(pid) {
			return nil, &ErrLocked{Path: path, HolderPID: pid}
		}

		// Stale lock — remove it and retry the atomic create.
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return nil, fmt.Errorf("pidlock: removing stale lock: %w", rmErr)
		}
		// Loop back to the top; another goroutine may win the next O_EXCL, which
		// is fine — we'll read their PID and return ErrLocked if they're alive.
	}
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
