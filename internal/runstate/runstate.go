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
)

// fileName is the sidecar's basename inside the config dir.
const fileName = "watcher.json"

// RunState describes the currently running weft watcher.
type RunState struct {
	PID       int       `json:"pid"`
	Profile   string    `json:"profile"`
	ConfigDir string    `json:"config_dir"`
	StartedAt time.Time `json:"started_at"`
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
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return fmt.Errorf("runstate: creating dir: %w", err)
	}
	data, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return fmt.Errorf("runstate: marshalling: %w", err)
	}
	dst := pathFor(cfgDir)
	tmp, err := os.CreateTemp(cfgDir, fileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("runstate: temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("runstate: writing: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("runstate: closing: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("runstate: renaming: %w", err)
	}
	return nil
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
	if !processAlive(rs.PID) {
		_ = Clear(cfgDir) // stale — the watcher crashed or was killed
		return nil, nil
	}
	return &rs, nil
}
