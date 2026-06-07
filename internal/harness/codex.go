package harness

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Codex adapts Weft to OpenAI Codex's ~/.codex layout.
// Codex reads AGENTS.md rather than CLAUDE.md.
type Codex struct{}

func (c *Codex) Name() string { return "codex" }

func (c *Codex) Detect() bool {
	if _, err := exec.LookPath("codex"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".codex"))
	return err == nil
}

// Apply copies files from stagedRoot into ~/.codex/, renaming CLAUDE.md → AGENTS.md.
func (c *Codex) Apply(stagedRoot string, ctx ApplyCtx) error {
	return applyToHomeDir(stagedRoot, ".codex", c.Name(), ctx, map[string]string{
		"CLAUDE.md": "AGENTS.md",
	})
}
