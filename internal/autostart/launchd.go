//go:build darwin

package autostart

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const launchdMechanism = "launchd LaunchAgent"

// launchdService installs weft as a per-user LaunchAgent. An *agent*, not a
// daemon: agents run in the user's GUI session with access to $HOME, which is
// exactly what the watcher needs.
type launchdService struct {
	cfgDir   string
	plistDir string
	domain   string // launchd service target domain, e.g. "gui/501"
	run      runner
	// tccProbe reports whether a TCC-protected path is readable. Injected so
	// tests need not depend on the host's privacy settings.
	tccProbe func() bool
}

func newService(cfgDir string) (Service, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("autostart: resolving home directory: %w", err)
	}
	return &launchdService{
		cfgDir:   cfgDir,
		plistDir: filepath.Join(home, "Library", "LaunchAgents"),
		domain:   "gui/" + strconv.Itoa(os.Getuid()),
		run:      execRunner,
		tccProbe: func() bool { return fullDiskAccessLikely(home) },
	}, nil
}

func (s *launchdService) plistPath() string {
	return filepath.Join(s.plistDir, Label+".plist")
}

func (s *launchdService) target() string { return s.domain + "/" + Label }

func (s *launchdService) Enable(o Options) error {
	if err := o.Validate(); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	plist, err := RenderLaunchdPlist(o)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.plistDir, 0o755); err != nil {
		return fmt.Errorf("autostart: creating LaunchAgents directory: %w", err)
	}
	// Bootout first so re-enabling picks up a changed binary path or profile.
	// A not-loaded agent makes this fail; that is the normal first-install case.
	_, _ = s.run("launchctl", "bootout", s.target())
	if err := os.WriteFile(s.plistPath(), []byte(plist), 0o644); err != nil {
		return fmt.Errorf("autostart: writing LaunchAgent: %w", err)
	}
	if _, err := s.run("launchctl", "bootstrap", s.domain, s.plistPath()); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	if _, err := s.run("launchctl", "kickstart", "-k", s.target()); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	return writeMeta(s.cfgDir, meta{
		Bin:         o.BinPath,
		ConfigFile:  o.ConfigFile,
		Profile:     o.Profile,
		Mechanism:   launchdMechanism,
		UnitPath:    s.plistPath(),
		InstalledAt: nowUTC(),
	})
}

func (s *launchdService) Disable() error {
	_, statErr := os.Stat(s.plistPath())
	missing := os.IsNotExist(statErr)

	// bootout while the plist still exists, then delete it — the reverse order
	// leaves launchd with a loaded job it can no longer resolve.
	if _, err := s.run("launchctl", "bootout", s.target()); err != nil && !missing {
		return fmt.Errorf("autostart: %w", err)
	}
	if !missing {
		if err := os.Remove(s.plistPath()); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("autostart: removing LaunchAgent: %w", err)
		}
	}
	if err := clearMeta(s.cfgDir); err != nil {
		return err
	}
	if missing {
		return ErrNotInstalled
	}
	return nil
}

func (s *launchdService) Status() (Status, error) {
	st := Status{Mechanism: launchdMechanism, UnitPath: s.plistPath()}

	plistText := ""
	if data, err := os.ReadFile(s.plistPath()); err == nil {
		st.Installed = true
		plistText = string(data)
	} else if !os.IsNotExist(err) {
		return st, fmt.Errorf("autostart: reading LaunchAgent: %w", err)
	}

	if st.Installed {
		// `launchctl print` exits non-zero when the job is not loaded, so the
		// output is read regardless of the error.
		out, _ := s.run("launchctl", "print", s.target())
		st.Running = strings.Contains(string(out), "state = running")
	}

	describe(s.cfgDir, &st, plistText)

	// Recent macOS gates parts of $HOME behind TCC. A LaunchAgent that cannot
	// read a watched path fails opaquely, so say so up front rather than
	// letting the user debug an empty apply.
	if st.Installed && s.tccProbe != nil && !s.tccProbe() {
		st.Notes = append(st.Notes,
			"macOS privacy protection may block some watched paths — grant Full Disk Access to the weft binary in System Settings › Privacy & Security if applies come up empty")
	}
	return st, nil
}

// fullDiskAccessLikely probes a TCC-protected directory. Readable means weft
// already has Full Disk Access (or the path is absent on this macOS version,
// which we also treat as "no reason to warn").
func fullDiskAccessLikely(home string) bool {
	probe := filepath.Join(home, "Library", "Application Support", "com.apple.TCC")
	if _, err := os.Stat(probe); os.IsNotExist(err) {
		return true
	}
	_, err := os.ReadDir(probe)
	return err == nil
}
