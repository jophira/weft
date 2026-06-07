package harness

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Aider adapts Weft to aider's conventions file.
// Aider has no standard global conventions path; this writes to ~/.aider/CONVENTIONS.md.
// Point aider at it with: conventions-file: ~/.aider/CONVENTIONS.md in ~/.aider.conf.yml.
type Aider struct{}

func (a *Aider) Name() string { return "aider" }

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

// Apply copies files from stagedRoot into ~/.aider/, renaming CLAUDE.md → CONVENTIONS.md.
func (a *Aider) Apply(stagedRoot string, ctx ApplyCtx) error {
	return applyToHomeDir(stagedRoot, ".aider", a.Name(), ctx, map[string]string{
		"CLAUDE.md": "CONVENTIONS.md",
	})
}
