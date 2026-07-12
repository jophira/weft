//go:build windows

package runstate

import "os"

// processAlive reports whether a process with the given pid currently exists.
// On Windows os.FindProcess opens a handle via OpenProcess and returns an error
// when the pid does not exist, so a successful open is treated as alive. This
// is best-effort (Windows support is non-blocking in weft); the flock lock
// remains the authoritative mutual-exclusion mechanism.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
