package harness

import (
	"os"
	"path/filepath"
)

// Cursor adapts Jophira to Cursor's .cursorrules layout.
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

// Apply writes the merged CLAUDE.md equivalent into .cursorrules.
func (c *Cursor) Apply(mergedRoot string) error {
	// TODO: implement merge into .cursorrules
	_ = mergedRoot
	return nil
}
