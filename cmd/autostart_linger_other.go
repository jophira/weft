//go:build !linux

package cmd

// defaultLingerHint is empty off Linux: launchd agents and Task Scheduler
// logon tasks have no linger equivalent, and --linger is a no-op there.
func defaultLingerHint() string { return "" }
