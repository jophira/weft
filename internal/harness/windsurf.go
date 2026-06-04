package harness

import (
	"fmt"
	"os"
	"path/filepath"
)

// Windsurf adapts Weft to Windsurf's global rules layout.
// Global rules live at ~/.codeium/windsurf/global_rules.md.
type Windsurf struct{}

func (w *Windsurf) Name() string { return "windsurf" }

func (w *Windsurf) Detect() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".codeium", "windsurf"))
	return err == nil
}

// Apply copies files from stagedRoot into ~/.codeium/windsurf/,
// renaming CLAUDE.md → global_rules.md.
func (w *Windsurf) Apply(stagedRoot string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}
	target := filepath.Join(home, ".codeium", "windsurf")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("ensuring ~/.codeium/windsurf exists: %w", err)
	}
	return copyWithRename(stagedRoot, target, map[string]string{
		"CLAUDE.md": "global_rules.md",
	})
}
