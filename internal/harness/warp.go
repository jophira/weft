package harness

import (
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

// warpYAMLFilter accepts only top-level .yaml/.yml files (no subdirectories).
// Warp workflows are a flat list of YAML files; subdirectory entries are skipped.
func warpYAMLFilter(rel string) bool {
	ext := filepath.Ext(rel)
	return (ext == ".yaml" || ext == ".yml") && filepath.Dir(rel) == "."
}

// Warp adapts Weft to Warp terminal's workflow layout.
type Warp struct{ detection }

func (w *Warp) Name() string { return "warp" }

// detectSignals declares no binary deliberately. Warp is a GUI terminal whose
// executable name differs per platform and is not reliably on PATH (it is
// warp-terminal on Linux, an .app bundle on macOS), so a lookup would produce
// false negatives that read as bugs. The config root is the honest signal.
func (w *Warp) detectSignals() detectSpec {
	return detectSpec{candidates: warpLocations}
}

func (w *Warp) Detect() bool { return w.run(w.detectSignals()) }

// ConfigPath implements ConfigPather: returns the resolved root when detected,
// or all OS-matching candidates joined by "  or  " otherwise.
func (w *Warp) ConfigPath() string {
	if root := w.detectedRoot(); root != "" {
		return locate.Tilde(root)
	}
	return locate.Display(warpLocations)
}

// Apply copies workflow YAML files from stagedRoot/commands/ into <configRoot>/workflows/.
// It delegates to applyWithManifest so that:
//   - conflict messages go to ctx.Out (not stdout), preventing MCP wire corruption;
//   - unchanged files are skipped via the fe.skip optimisation;
//   - the walk is performed exactly once.
func (w *Warp) Apply(stagedRoot string, ctx ApplyCtx) error {
	root := w.detectedRoot()
	if root == "" {
		w.Detect()
		if root = w.detectedRoot(); root == "" {
			// Not yet installed; default to the platform-primary location.
			root = locate.All(warpLocations)[0]
		}
	}
	target := filepath.Join(root, "workflows")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	src := filepath.Join(stagedRoot, "commands")
	// Harness is nil deliberately: Warp re-roots the staged tree at commands/ and
	// filters to YAML itself, so paths reaching applyWithManifest carry no class
	// prefix to route on. Its placement decision has already been made here.
	return applyWithManifest(src, target, w.Name(), ctx, nil, warpYAMLFilter, nil)
}
