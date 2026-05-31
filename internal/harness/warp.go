package harness

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Warp adapts Weft to Warp terminal's workflow layout (~/.warp/workflows/).
type Warp struct{}

func (w *Warp) Name() string { return "warp" }

func (w *Warp) Detect() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".warp"))
	return err == nil
}

// Apply copies workflow YAML files from stagedRoot/commands/ into ~/.warp/workflows/.
// Non-YAML files and other subdirectories are skipped.
func (w *Warp) Apply(stagedRoot string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	target := filepath.Join(home, ".warp", "workflows")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}

	src := filepath.Join(stagedRoot, "commands")
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(d.Name()) != ".yaml" && filepath.Ext(d.Name()) != ".yml" {
			return nil
		}
		dst := filepath.Join(target, d.Name())
		return copyFile(path, dst)
	})
}
