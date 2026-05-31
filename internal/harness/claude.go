package harness

import (
	"fmt"
	"io/fs"
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

	return filepath.WalkDir(stagedRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stagedRoot, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(target, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("creating parent dir for %s: %w", rel, err)
		}
		return copyFile(path, dst)
	})
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	return os.WriteFile(dst, data, 0o644)
}
