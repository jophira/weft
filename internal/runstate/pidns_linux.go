//go:build linux

package runstate

import "os"

// pidNamespace returns an opaque identifier for the calling process's PID
// namespace, e.g. "pid:[4026531836]". The kernel exposes it as a magic symlink
// whose target is the namespace's inode number, so two processes sharing a
// namespace read the same string and processes in different ones do not.
//
// Returns "" when /proc is not mounted or the link cannot be read; callers
// treat that the same way they treat a platform without namespaces.
// (cf. Java: no stdlib equivalent, this is a Linux kernel interface.)
func pidNamespace() string {
	ns, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return ""
	}
	return ns
}
