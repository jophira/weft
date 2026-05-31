package harness

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// GenericHarness handles tools whose apply strategy is a plain directory copy.
// Most AI coding tools fall into this category — detect a path or binary,
// then copy the staged output into a config directory under $HOME.
type GenericHarness struct {
	name         string
	detectPath   string // relative to $HOME; empty = skip path check
	detectBinary string // binary name looked up via PATH; empty = skip
	configDir    string // relative to $HOME; destination for Apply
}

func (g *GenericHarness) Name() string { return g.name }

func (g *GenericHarness) Detect() bool {
	if g.detectBinary != "" {
		if _, err := exec.LookPath(g.detectBinary); err == nil {
			return true
		}
	}
	if g.detectPath != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		_, err = os.Stat(filepath.Join(home, g.detectPath))
		return err == nil
	}
	return false
}

func (g *GenericHarness) Apply(stagedRoot string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}
	target := filepath.Join(home, g.configDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("ensuring %s exists: %w", target, err)
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
