// Package testenv provides small cross-platform helpers for tests that need to
// manipulate the process environment hermetically.
package testenv

import "testing"

// SetHome points the user's home directory at dir for the duration of the test.
//
// os.UserHomeDir resolves $HOME on Unix but %USERPROFILE% on Windows, so a test
// that sets only HOME silently writes to the real Windows profile instead of the
// temp dir. Setting both keeps tests hermetic on every OS. t.Setenv restores the
// previous values automatically at test cleanup.
func SetHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// ClearPath points $PATH at an empty directory for the duration of the test.
//
// Harness detection falls back to looking for a tool's binary on PATH, so a test
// that isolates only HOME still sees whatever the developer happens to have
// installed — "no harnesses" then means something different on a laptop than in
// CI. An empty directory is used rather than an empty string because Go's
// exec.LookPath reads an empty PATH entry as the current directory.
func ClearPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}
