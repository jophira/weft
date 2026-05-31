package harness

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Aider adapts Weft to aider's configuration (~/.aider.conf.yml).
type Aider struct{}

func (a *Aider) Name() string { return "aider" }

// Detect returns true if either the aider binary is in PATH or
// a configuration file exists in the home directory.
func (a *Aider) Detect() bool {
	if _, err := exec.LookPath("aider"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".aider.conf.yml"))
	return err == nil
}

// Apply writes the merged CLAUDE.md content as an aider system-prompt
// convention file. Full implementation is a future TODO.
func (a *Aider) Apply(mergedRoot string) error {
	_ = mergedRoot
	return nil
}
