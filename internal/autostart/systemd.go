//go:build linux

package autostart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const systemdMechanism = "systemd user unit"

// systemdService installs weft as a systemd *user* unit. A user unit — not a
// system one — because weft manages files under $HOME and must run as the
// user, and because installing it needs no root.
type systemdService struct {
	cfgDir  string
	unitDir string
	run     runner
}

func newService(cfgDir string) (Service, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("autostart: resolving home directory: %w", err)
	}
	return &systemdService{
		cfgDir:  cfgDir,
		unitDir: filepath.Join(home, ".config", "systemd", "user"),
		run:     execRunner,
	}, nil
}

func (s *systemdService) unitPath() string { return filepath.Join(s.unitDir, UnitName) }

func (s *systemdService) Enable(o Options) error {
	if err := o.Validate(); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	if err := os.MkdirAll(s.unitDir, 0o755); err != nil {
		return fmt.Errorf("autostart: creating unit directory: %w", err)
	}
	if err := os.WriteFile(s.unitPath(), []byte(RenderSystemdUnit(o)), 0o644); err != nil {
		return fmt.Errorf("autostart: writing unit: %w", err)
	}
	// If systemd rejects the unit, take the file back out. A half-installed
	// unit — present on disk but never enabled — is exactly the orphan state
	// `disable` is supposed to guarantee cannot happen.
	if err := s.activate(); err != nil {
		_ = os.Remove(s.unitPath())
		_, _ = s.run("systemctl", "--user", "daemon-reload")
		return err
	}
	// Linger keeps user services running with no login session (headless
	// servers, or after logging out). It changes machine-wide policy for the
	// user, so it is only ever done on explicit request.
	if o.Linger {
		if _, err := s.run("loginctl", "enable-linger"); err != nil {
			return fmt.Errorf("autostart: enabling linger: %w", err)
		}
	}
	return writeMeta(s.cfgDir, meta{
		Bin:         o.BinPath,
		ConfigFile:  o.ConfigFile,
		Profile:     o.Profile,
		Mechanism:   systemdMechanism,
		UnitPath:    s.unitPath(),
		InstalledAt: nowUTC(),
	})
}

// activate makes systemd pick up the freshly written unit and start it.
func (s *systemdService) activate() error {
	if _, err := s.run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	if _, err := s.run("systemctl", "--user", "enable", "--now", UnitName); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	return nil
}

func (s *systemdService) Disable() error {
	_, statErr := os.Stat(s.unitPath())
	missing := os.IsNotExist(statErr)

	// Stop and unregister before deleting the file: removing it first would
	// leave systemd holding a unit it can no longer resolve, and `disable`
	// would then fail to clean up the symlink in default.target.wants.
	if !missing {
		if _, err := s.run("systemctl", "--user", "disable", "--now", UnitName); err != nil {
			return fmt.Errorf("autostart: %w", err)
		}
		if err := os.Remove(s.unitPath()); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("autostart: removing unit: %w", err)
		}
	}
	// daemon-reload unconditionally so a unit removed by hand also stops
	// showing up in `systemctl --user list-unit-files`.
	if _, err := s.run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	if _, err := s.run("systemctl", "--user", "reset-failed", UnitName); err != nil {
		// A unit that never failed has nothing to reset — not an error.
		_ = err
	}
	if err := clearMeta(s.cfgDir); err != nil {
		return err
	}
	if missing {
		return ErrNotInstalled
	}
	return nil
}

func (s *systemdService) Status() (Status, error) {
	st := Status{Mechanism: systemdMechanism, UnitPath: s.unitPath()}

	unitText := ""
	if data, err := os.ReadFile(s.unitPath()); err == nil {
		st.Installed = true
		unitText = string(data)
	} else if !os.IsNotExist(err) {
		return st, fmt.Errorf("autostart: reading unit: %w", err)
	}

	if st.Installed {
		// is-active exits non-zero when inactive, so the output — not the
		// error — is the answer. cf. Java: reading the process output rather
		// than treating a non-zero exit as an exception.
		out, _ := s.run("systemctl", "--user", "is-active", UnitName)
		st.Running = strings.TrimSpace(string(out)) == "active"
	}

	describe(s.cfgDir, &st, unitText)
	return st, nil
}
