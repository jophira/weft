package harness

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
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
func (w *Warp) Apply(stagedRoot string, ctx ApplyCtx) error {
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

	m, err := manifest.Load(ctx.CfgDir, w.Name())
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	var conflicts []conflictFile
	newFiles := map[string]string{}

	walkErr := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := filepath.Ext(d.Name())
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		rel := d.Name() // flat copy — just the filename, no subdirs
		stagedHash, err := manifest.HashFile(path)
		if err != nil {
			return err
		}
		newFiles[rel] = stagedHash

		fullDst := filepath.Join(target, rel)
		existing, readErr := os.ReadFile(fullDst)
		if os.IsNotExist(readErr) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", fullDst, readErr)
		}
		if knownHash, owned := m.Files[rel]; owned && manifest.HashBytes(existing) == knownHash {
			return nil
		}
		conflicts = append(conflicts, conflictFile{rel: rel, abs: fullDst})
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	if len(conflicts) > 0 {
		backupDir, err := backupConflicts(conflicts, w.Name(), ctx.CfgDir)
		if err != nil {
			return err
		}
		fmt.Printf("  ! %d file(s) externally modified — backed up to %s\n",
			len(conflicts), locate.Tilde(backupDir))
		for _, c := range conflicts {
			fmt.Printf("      %s\n", c.rel)
		}
	}

	// Write yaml files.
	if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := filepath.Ext(d.Name()); ext != ".yaml" && ext != ".yml" {
			return nil
		}
		return copyFile(path, filepath.Join(target, d.Name()))
	}); err != nil {
		return err
	}

	m.Harness = w.Name()
	m.Profile = ctx.ProfileName
	m.TargetRoot = target
	m.AppliedAt = time.Now()
	maps.Copy(m.Files, newFiles)
	return manifest.Save(ctx.CfgDir, m)
}
