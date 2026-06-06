package harness

import (
	"fmt"
	"os"
	"path/filepath"
)

// Cursor adapts Weft to Cursor's global rules layout.
// Global rules live in ~/.cursor/rules/ as .mdc files with YAML frontmatter.
type Cursor struct{}

func (c *Cursor) Name() string { return "cursor" }

func (c *Cursor) Detect() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".cursor"))
	return err == nil
}

const cursorMDCHeader = "---\nalwaysApply: true\n---\n"

// Apply writes CLAUDE.md to ~/.cursor/rules/weft.mdc with always-apply frontmatter.
// Other staged files (commands, hooks, etc.) have no Cursor global equivalent and are skipped.
func (c *Cursor) Apply(stagedRoot string, ctx ApplyCtx) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}
	rulesDir := filepath.Join(home, ".cursor", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		return fmt.Errorf("ensuring ~/.cursor/rules exists: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(stagedRoot, "CLAUDE.md"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading CLAUDE.md: %w", err)
	}
	content := append([]byte(cursorMDCHeader), data...)
	dst := filepath.Join(rulesDir, "weft.mdc")
	return trackAndWriteFile(dst, "weft.mdc", c.Name(), content, ctx)
}
