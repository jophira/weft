//go:build !windows

package runstate

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether a process with the given pid currently exists.
// Signal 0 performs the kernel's permission/existence checks without delivering
// a signal: nil means alive, ESRCH means gone, EPERM means alive but owned by
// another user (still counts as alive). cf. Java: no stdlib equivalent — would
// need ProcessHandle.of(pid).isPresent() on Java 9+.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid) // never fails on Unix
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
