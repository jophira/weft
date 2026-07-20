//go:build windows

package autostart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const schtasksMechanism = "Task Scheduler logon task"

// schtasksService registers weft as a Task Scheduler task that fires at logon.
// Deliberately not the Startup folder or HKCU\Software\Microsoft\Windows\
// CurrentVersion\Run: both start weft as a visible console process, flashing a
// window on every boot. A hidden task does not.
type schtasksService struct {
	cfgDir string
	// defPath is where the generated task XML is kept. Task Scheduler copies
	// the definition on import, so this file is only weft's own record —
	// keeping it lets `status` show the user what was registered.
	defPath string
	run     runner
}

func newService(cfgDir string) (Service, error) {
	return &schtasksService{
		cfgDir:  cfgDir,
		defPath: filepath.Join(cfgDir, "autostart-task.xml"),
		run:     execRunner,
	}, nil
}

func (s *schtasksService) Enable(o Options) error {
	if err := o.Validate(); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	taskXML, err := RenderTaskXML(o)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.cfgDir, 0o755); err != nil {
		return fmt.Errorf("autostart: creating config dir: %w", err)
	}
	// schtasks /Create /XML requires UTF-16LE with a BOM; it rejects UTF-8.
	if err := os.WriteFile(s.defPath, EncodeUTF16LE(taskXML), 0o644); err != nil {
		return fmt.Errorf("autostart: writing task definition: %w", err)
	}
	// /F overwrites an existing task, making enable idempotent.
	if _, err := s.run("schtasks", "/Create", "/TN", TaskName, "/XML", s.defPath, "/F"); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	if _, err := s.run("schtasks", "/Run", "/TN", TaskName); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	return writeMeta(s.cfgDir, meta{
		Bin:         o.BinPath,
		ConfigFile:  o.ConfigFile,
		Profile:     o.Profile,
		Mechanism:   schtasksMechanism,
		UnitPath:    s.defPath,
		InstalledAt: nowUTC(),
	})
}

func (s *schtasksService) Disable() error {
	installed := s.registered()
	if installed {
		// /End first so a running watcher is stopped rather than orphaned when
		// the task registration disappears underneath it.
		_, _ = s.run("schtasks", "/End", "/TN", TaskName)
		if _, err := s.run("schtasks", "/Delete", "/TN", TaskName, "/F"); err != nil {
			return fmt.Errorf("autostart: %w", err)
		}
	}
	if err := os.Remove(s.defPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("autostart: removing task definition: %w", err)
	}
	if err := clearMeta(s.cfgDir); err != nil {
		return err
	}
	if !installed {
		return ErrNotInstalled
	}
	return nil
}

func (s *schtasksService) Status() (Status, error) {
	st := Status{Mechanism: schtasksMechanism, UnitPath: s.defPath}

	out, err := s.run("schtasks", "/Query", "/TN", TaskName, "/FO", "LIST")
	if err == nil {
		st.Installed = true
		st.Running = strings.Contains(string(out), "Status:") &&
			strings.Contains(string(out), "Running")
	}

	// The registered task is the authority, but its XML lives inside Task
	// Scheduler. Cross-check against weft's own copy of what it submitted.
	defText := ""
	if data, err := os.ReadFile(s.defPath); err == nil {
		defText = DecodeUTF16LE(data)
	}
	describe(s.cfgDir, &st, defText)

	// fsnotify on a OneDrive-synced home sees every cloud placeholder
	// materialisation as a write, which can pin the watcher at 100% CPU.
	if st.Installed {
		if home, err := os.UserHomeDir(); err == nil && strings.Contains(strings.ToLower(home), "onedrive") {
			st.Notes = append(st.Notes,
				"home directory is OneDrive-synced — cloud sync can generate file-change storms; consider moving weft sources outside the synced tree")
		}
	}
	return st, nil
}

// registered reports whether Task Scheduler knows the task.
func (s *schtasksService) registered() bool {
	_, err := s.run("schtasks", "/Query", "/TN", TaskName, "/FO", "LIST")
	return err == nil
}
