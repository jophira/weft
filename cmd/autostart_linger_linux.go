//go:build linux

package cmd

// defaultLingerHint explains the one Linux-specific gap in the default setup:
// without linger, systemd tears down the user manager — and the watcher with
// it — when the last login session ends. That is the right default for a
// laptop and the wrong one for a headless box, so weft states the trade-off
// instead of silently enabling a machine-wide policy change.
func defaultLingerHint() string {
	return "Note: the watcher stops when your last login session ends. " +
		"Re-run with --linger to keep it running on a headless machine."
}
