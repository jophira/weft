package harness

import (
	"fmt"
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
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}
	target := filepath.Join(home, ".aider")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("ensuring ~/.aider exists: %w", err)
	}
	return applyWithManifest(stagedRoot, target, a.Name(), ctx, map[string]string{
		"CLAUDE.md": "CONVENTIONS.md",
	})
}
