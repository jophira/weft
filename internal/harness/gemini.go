package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// GeminiCLI adapts Weft to Gemini CLI's ~/.gemini layout.
// Gemini CLI reads GEMINI.md rather than CLAUDE.md.
type GeminiCLI struct{}

func (g *GeminiCLI) Name() string { return "gemini-cli" }

func (g *GeminiCLI) Detect() bool {
	if _, err := exec.LookPath("gemini"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".gemini"))
	return err == nil
}

// Apply copies files from stagedRoot into ~/.gemini/, renaming CLAUDE.md → GEMINI.md.
func (g *GeminiCLI) Apply(stagedRoot string, ctx ApplyCtx) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}
	target := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("ensuring ~/.gemini exists: %w", err)
	}
	return applyWithManifest(stagedRoot, target, g.Name(), ctx, map[string]string{
		"CLAUDE.md": "GEMINI.md",
	})
}
