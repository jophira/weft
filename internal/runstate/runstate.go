// Package runstate records a small sidecar file describing the live weft
// watcher (pid, active profile, config dir, start time). The flock-based
// singleton lock (internal/pidlock) proves *that* a watcher is running but,
// because flock(2) exposes no holder PID to user-space, cannot say *which* one.
// This file fills that gap so commands can report a rich "already running"
// message and `weft status` can show watcher state.
//
// The file is advisory, not authoritative: the lock remains the source of truth
// for mutual exclusion. A crash (or SIGKILL) leaves the file behind, so Read
// verifies the recorded process is still alive and treats a dead pid as
// "not running", removing the stale file as it goes.
package runstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jophira/weft/internal/privatefile"
)

// fileName is the sidecar's basename inside the config dir.
const fileName = "watcher.json"

// RunState describes the currently running weft watcher.
type RunState struct {
	PID       int       `json:"pid"`
	Profile   string    `json:"profile"`
	ConfigDir string    `json:"config_dir"`
	StartedAt time.Time `json:"started_at"`
	// PIDNamespace identifies the namespace PID was allocated in. A reader in a
	// different namespace cannot see that pid and must not read its absence as
	// death. Empty means unknown: a platform without PID namespaces, or a
	// sidecar written before this field existed.
	PIDNamespace string `json:"pid_namespace,omitempty"`
}

// Uptime reports how long the watcher has been running.
func (r RunState) Uptime() time.Duration {
	return time.Since(r.StartedAt)
}

// pathFor returns the sidecar path for a config dir.
func pathFor(cfgDir string) string {
	return filepath.Join(cfgDir, fileName)
}

// Write records rs to cfgDir/watcher.json, replacing any previous contents.
// The write is atomic (temp file + rename) so a concurrent Read never observes
// a half-written file. cf. Java: Files.move(tmp, dst, ATOMIC_MOVE).
func Write(cfgDir string, rs RunState) error {
	rs.PIDNamespace = pidNamespace()
	if err := privatefile.MkdirAll(cfgDir); err != nil {
		return fmt.Errorf("runstate: creating dir: %w", err)
	}
	data, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return fmt.Errorf("runstate: marshalling: %w", err)
	}
	return privatefile.Write(filepath.Join(cfgDir, fileName), data)
}

// Clear removes the sidecar. A missing file is not an error — clearing is
// best-effort cleanup on watcher shutdown.
func Clear(cfgDir string) error {
	err := os.Remove(pathFor(cfgDir))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("runstate: removing: %w", err)
	}
	return nil
}

// Read returns the live watcher's state for cfgDir, or nil when no live watcher
// owns it — the file is absent, unreadable, or the recorded process is gone.
// A stale file (dead pid) is removed as a side effect so it does not linger.
// The liveness check only runs when the caller shares the writer's PID
// namespace; see the comment at the check for why.
func Read(cfgDir string) (*RunState, error) {
	data, err := os.ReadFile(pathFor(cfgDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("runstate: reading: %w", err)
	}
	var rs RunState
	if err := json.Unmarshal(data, &rs); err != nil {
		// A corrupt sidecar is not authoritative; drop it and report "none".
		_ = Clear(cfgDir)
		return nil, nil
	}
	// A pid only means something inside the namespace that allocated it. Read
	// from another one (a container, a sandboxed shell) the watcher's pid is
	// invisible, and processAlive would report a live watcher as dead and then
	// delete its sidecar. Across that boundary the file is all we have, so
	// trust it and leave it in place. The cost is that a crashed watcher reads
	// as running until something in its own namespace clears the file.
	//
	// Both ids have to be known for the boundary to be real. An empty one is a
	// legacy sidecar or a platform without namespaces, and there the pid check
	// is the best answer available.
	if !crossesNamespace(rs.PIDNamespace, pidNamespace()) && !processAlive(rs.PID) {
		_ = Clear(cfgDir) // stale — the watcher crashed or was killed
		return nil, nil
	}
	return &rs, nil
}

// crossesNamespace reports whether recorded and current name two different,
// known PID namespaces. Either one empty means unknown, which is not a
// boundary we can act on.
func crossesNamespace(recorded, current string) bool {
	return recorded != "" && current != "" && recorded != current
}
