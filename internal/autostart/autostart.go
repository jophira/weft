// Package autostart installs, removes, and inspects the per-user background
// service that keeps the weft watcher alive across reboots.
//
// It is strictly opt-in: nothing here runs unless the user invokes
// `weft autostart enable`. Weft never installs a service as a side effect of
// another command.
//
// The service is only safe to offer because the singleton lock
// (internal/pidlock) and the runstate sidecar (internal/runstate) already
// exist. The lock stops an autostarted watcher and a hand-launched
// `weft profile use` from double-watching; runstate lets `weft status` say
// which watcher is live.
//
// Each platform gets its own mechanism — a systemd *user* unit on Linux, a
// LaunchAgent on macOS, a Task Scheduler logon task on Windows — behind one
// Service interface. cf. Java: an interface with per-OS implementations
// selected by a factory, except the selection happens at compile time via
// build tags rather than at runtime.
package autostart

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Label is the reverse-DNS service identifier used by launchd, and the
	// stem of the systemd unit and scheduled-task names.
	Label = "com.jophira.weft"

	// UnitName is the systemd user unit filename.
	UnitName = "weft.service"

	// TaskName is the Windows Task Scheduler task name.
	TaskName = "weft"

	// metaFile is the sidecar, inside the config dir, recording what weft
	// installed. It lets `status` answer "which binary / which profile" the
	// same way on every platform instead of parsing three unit formats.
	metaFile = "autostart.json"
)

// ErrUnsupported is returned by New on platforms with no autostart mechanism.
var ErrUnsupported = errors.New("autostart is not supported on this platform")

// ErrNotInstalled is returned by Disable when there is nothing to remove.
var ErrNotInstalled = errors.New("autostart is not installed")

// Options describes the unit to install.
type Options struct {
	// BinPath is the absolute path to the weft binary the unit invokes.
	BinPath string
	// ConfigFile mirrors the caller's --config so an isolated config stays
	// isolated after a reboot. Empty means the default ~/.config/weft/config.yaml.
	ConfigFile string
	// Profile pins the unit to one profile. Empty selects follow-active mode:
	// the unit resolves active_profile from config.yaml at start, so the
	// machine comes back up on whatever profile was last switched to.
	Profile string
	// Linger requests `loginctl enable-linger` (Linux only) so the watcher
	// runs without an active login session. Never enabled implicitly.
	Linger bool
}

// Validate reports whether the options can produce a working unit.
func (o Options) Validate() error {
	if o.BinPath == "" {
		return errors.New("binary path is empty")
	}
	if !filepath.IsAbs(o.BinPath) {
		return fmt.Errorf("binary path %q is not absolute", o.BinPath)
	}
	if _, err := os.Stat(o.BinPath); err != nil {
		return fmt.Errorf("binary path %q is not usable: %w", o.BinPath, err)
	}
	if o.ConfigFile != "" && !filepath.IsAbs(o.ConfigFile) {
		return fmt.Errorf("config file %q is not absolute", o.ConfigFile)
	}
	return nil
}

// Status is what `weft autostart status` reports.
type Status struct {
	// Mechanism names the OS facility in use, e.g. "systemd user unit".
	Mechanism string
	// UnitPath is the unit/plist/task-definition location, for the user to inspect.
	UnitPath string
	// Installed reports whether the unit exists on disk / is registered.
	Installed bool
	// Running reports whether the service is currently active.
	Running bool
	// Profile is the pinned profile, or "" for follow-active mode.
	Profile string
	// BinPath is the weft binary the unit invokes.
	BinPath string
	// Stale means the recorded binary no longer exists — the unit would fail
	// on every boot. The user is told to re-run `weft autostart enable`.
	Stale bool
	// Notes carries advisory findings (binary moved, OneDrive home, TCC).
	Notes []string
}

// ProfileMode renders the profile-selection mode for display.
func (s Status) ProfileMode() string {
	if s.Profile == "" {
		return "follow-active"
	}
	return "pinned"
}

// Service manages the platform's autostart unit.
type Service interface {
	// Enable installs and starts the unit, replacing any unit weft installed
	// earlier. It is idempotent.
	Enable(o Options) error
	// Disable stops and removes the unit, leaving no orphan behind. It
	// returns ErrNotInstalled when there is nothing to remove.
	Disable() error
	// Status reports the current installation.
	Status() (Status, error)
}

// New returns the Service for the running platform, rooted at cfgDir (weft's
// config directory — where the metadata sidecar lives).
func New(cfgDir string) (Service, error) {
	if cfgDir == "" {
		return nil, errors.New("autostart: config directory is empty")
	}
	return newService(cfgDir)
}

// RunArgs returns the arguments the installed unit passes to the weft binary.
// Kept here, not in the platform files, so all three units agree on the
// command line and one test covers it.
func RunArgs(o Options) []string {
	args := []string{"autostart", "run"}
	if o.ConfigFile != "" {
		args = append(args, "--config", o.ConfigFile)
	}
	if o.Profile != "" {
		args = append(args, "--profile", o.Profile)
	}
	return args
}

// runner executes an external command and returns its combined output. It is a
// field on every platform service so tests can substitute a fake instead of
// invoking systemctl/launchctl/schtasks for real.
type runner func(name string, args ...string) ([]byte, error)

// execRunner is the production runner.
func execRunner(name string, args ...string) ([]byte, error) {
	// #nosec G204 -- command names are compile-time constants and arguments are
	// built from validated Options, never from unsanitised user input.
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// meta is the sidecar recording what weft installed.
type meta struct {
	Bin         string    `json:"bin"`
	ConfigFile  string    `json:"config_file,omitempty"`
	Profile     string    `json:"profile,omitempty"`
	Mechanism   string    `json:"mechanism"`
	UnitPath    string    `json:"unit_path"`
	InstalledAt time.Time `json:"installed_at"`
}

func metaPath(cfgDir string) string { return filepath.Join(cfgDir, metaFile) }

// nowUTC timestamps the metadata sidecar. Wrapped so the platform files share
// one convention (UTC, never local time).
func nowUTC() time.Time { return time.Now().UTC() }

func writeMeta(cfgDir string, m meta) error {
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return fmt.Errorf("autostart: creating config dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("autostart: marshalling metadata: %w", err)
	}
	if err := os.WriteFile(metaPath(cfgDir), data, 0o644); err != nil {
		return fmt.Errorf("autostart: writing metadata: %w", err)
	}
	return nil
}

// readMeta returns the sidecar, or nil when it is absent or unreadable. A
// corrupt sidecar is treated as absent: it is a cache of what we installed,
// never the source of truth for whether a unit exists.
func readMeta(cfgDir string) *meta {
	data, err := os.ReadFile(metaPath(cfgDir))
	if err != nil {
		return nil
	}
	var m meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return &m
}

func clearMeta(cfgDir string) error {
	err := os.Remove(metaPath(cfgDir))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("autostart: removing metadata: %w", err)
	}
	return nil
}

// describe folds the metadata sidecar into st and flags the two binary-path
// problems the unit cannot detect for itself: a recorded binary that no longer
// exists (fails on every boot), and a unit pointing at a different weft than
// the one being run right now (easy to hit with ~/go/bin plus a repo ./bin).
// unitText is the raw unit/plist/XML, used to confirm it really names that binary.
func describe(cfgDir string, st *Status, unitText string) {
	m := readMeta(cfgDir)
	if m == nil {
		if st.Installed {
			st.Notes = append(st.Notes, "unit exists but weft has no record of installing it — re-run 'weft autostart enable' to take ownership")
		}
		return
	}
	st.BinPath = m.Bin
	st.Profile = m.Profile

	if _, err := os.Stat(m.Bin); err != nil {
		st.Stale = true
		st.Notes = append(st.Notes,
			fmt.Sprintf("recorded binary %s no longer exists — re-run 'weft autostart enable' to re-point the unit", m.Bin))
	} else if self, err := os.Executable(); err == nil && isRivalBinary(self, m.Bin) {
		st.Notes = append(st.Notes,
			fmt.Sprintf("unit runs %s but you are running %s — re-run 'weft autostart enable' to re-point it", m.Bin, self))
	}

	if st.Installed && unitText != "" && !strings.Contains(unitText, m.Bin) {
		st.Stale = true
		st.Notes = append(st.Notes,
			"installed unit does not reference the recorded binary — it was edited outside weft; re-run 'weft autostart enable'")
	}
}

// isRivalBinary reports whether self and recorded are two *different copies of
// weft* — the situation the user actually wants flagged (a unit pinned to
// ~/go/bin/weft while they run a repo ./bin/weft). Programs with different
// basenames are not rivals: comparing them says nothing useful, and doing so
// would make every `go test` binary look like a mismatch.
func isRivalBinary(self, recorded string) bool {
	if filepath.Base(self) != filepath.Base(recorded) {
		return false
	}
	return !sameFile(self, recorded)
}

// sameFile compares two paths by inode where possible, falling back to
// resolved-path equality. Two different paths can be the same binary (a
// symlink from ~/.local/bin into a build tree), and warning about that would
// be noise.
func sameFile(a, b string) bool {
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr == nil && berr == nil && os.SameFile(ai, bi) {
		return true
	}
	ra, aerr := filepath.EvalSymlinks(a)
	rb, berr := filepath.EvalSymlinks(b)
	return aerr == nil && berr == nil && ra == rb
}

// WaitForPath blocks until path exists, or timeout elapses.
//
// A user unit can fire before a network or encrypted $HOME is mounted; without
// this the watcher would start against an empty home and — under
// Restart=on-failure — churn until the mount appeared. Polling is the right
// tool here: there is no portable way to wait on an arbitrary mount from user
// space.
//
// Callers should wait on a *file weft itself wrote* (config.yaml), not on the
// home directory. Waiting on the directory proves nothing: any process that
// calls MkdirAll on a path under an unmounted home — weft's own logger among
// them — creates it, so its presence is not evidence the real home is there.
func WaitForPath(path string, timeout, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("autostart: poll interval must be positive")
	}
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("autostart: %s did not become available within %s", path, timeout)
		}
		time.Sleep(interval)
	}
}
