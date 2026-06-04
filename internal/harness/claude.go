package harness

import (
	"fmt"
	"os"
	"path/filepath"
)

// ClaudeCode adapts Weft to Claude Code's ~/.claude layout.
type ClaudeCode struct{}

func (c *ClaudeCode) Name() string { return "claude-code" }

func (c *ClaudeCode) Detect() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".claude"))
	return err == nil
}

// Apply copies every file from stagedRoot into ~/.claude/, creating
// subdirectories as needed. Existing files are overwritten; files not present
// in stagedRoot are left untouched.
func (c *ClaudeCode) Apply(stagedRoot string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}
	target := filepath.Join(home, ".claude")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("ensuring ~/.claude exists: %w", err)
	}
	return copyWithRename(stagedRoot, target, nil)
}
