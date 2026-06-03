package harness

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jophira/weft/internal/locate"
)

// warpLocations lists every known config root for Warp, most specific first.
//
// Warp has used different roots across versions and platforms:
//   - Linux (current):  $XDG_CONFIG_HOME/warp-terminal  (usually ~/.config/warp-terminal)
//   - macOS (current):  ~/Library/Application Support/warp-terminal
//   - macOS (legacy):   ~/.warp
var warpLocations = []locate.Candidate{
	{
		Path: func(_, xdg string) string { return filepath.Join(xdg, "warp-terminal") },
		GOOS: []string{"linux"},
	},
	{
		Path: func(home, _ string) string {
			return filepath.Join(home, "Library", "Application Support", "warp-terminal")
		},
		GOOS: []string{"darwin"},
	},
	{
		Path: func(home, _ string) string { return filepath.Join(home, ".warp") },
	},
}

// Warp adapts Weft to Warp terminal's workflow layout.
type Warp struct {
	configRoot string // resolved by Detect
}

func (w *Warp) Name() string { return "warp" }

func (w *Warp) Detect() bool {
	p, ok := locate.First(warpLocations)
	if ok {
		w.configRoot = p
	}
	return ok
}

// ConfigPath implements ConfigPather: returns the resolved root when detected,
// or all OS-matching candidates joined by "  or  " otherwise.
func (w *Warp) ConfigPath() string {
	if w.configRoot != "" {
		return locate.Tilde(w.configRoot)
	}
	return locate.Display(warpLocations)
}

// Apply copies workflow YAML files from stagedRoot/commands/ into <configRoot>/workflows/.
func (w *Warp) Apply(stagedRoot string) error {
	if w.configRoot == "" {
		if !w.Detect() {
			// Not yet installed; default to the platform-primary location.
			w.configRoot = locate.All(warpLocations)[0]
		}
	}
	target := filepath.Join(w.configRoot, "workflows")
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
		return copyFile(path, filepath.Join(target, d.Name()))
	})
}
