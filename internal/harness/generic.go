package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jophira/weft/internal/locate"
)

// GenericHarness handles tools whose apply strategy is a plain directory copy.
// Most AI coding tools fall into this category — detect a config root or binary,
// then copy the staged output into the resolved directory.
type GenericHarness struct {
	name         string
	detectBinary string             // binary looked up via PATH; empty = skip
	candidates   []locate.Candidate // config root candidates; probed in order
	root         string             // resolved by Detect; used by Apply
}

func (g *GenericHarness) Name() string { return g.name }

func (g *GenericHarness) Detect() bool {
	// Prefer an existing config directory — it pinpoints the exact root to write.
	if p, ok := locate.First(g.candidates); ok {
		g.root = p
		return true
	}
	// Fall back to binary detection; prime root with the first candidate path
	// so Apply knows where to create the directory.
	if g.detectBinary != "" {
		if _, err := exec.LookPath(g.detectBinary); err == nil {
			if paths := locate.All(g.candidates); len(paths) > 0 {
				g.root = paths[0]
			}
			return true
		}
	}
	return false
}

// ConfigPath implements ConfigPather: returns the resolved root when detected,
// or the full candidate display string otherwise.
func (g *GenericHarness) ConfigPath() string {
	if g.root != "" {
		return locate.Tilde(g.root)
	}
	return locate.Display(g.candidates)
}

// InstructionSpec: directory-copy harnesses have no known include directive, so
// weft inlines content (Tier B) into <root>/CLAUDE.md. The default for any
// unknown or user-defined harness — the safe, fool-proof fallback.
func (g *GenericHarness) InstructionSpec() (InstructionSpec, error) {
	if g.root == "" {
		if !g.Detect() {
			return InstructionSpec{}, fmt.Errorf("%s not detected — install it or create its config directory", g.name)
		}
	}
	return InstructionSpec{Path: filepath.Join(g.root, "CLAUDE.md"), Strategy: StrategyInline}, nil
}

func (g *GenericHarness) Apply(stagedRoot string, ctx ApplyCtx) error {
	if g.root == "" {
		if !g.Detect() {
			return fmt.Errorf("%s not detected — install it or create its config directory", g.name)
		}
	}
	if err := os.MkdirAll(g.root, 0o755); err != nil {
		return fmt.Errorf("ensuring %s exists: %w", g.root, err)
	}
	return applyWithManifest(stagedRoot, g.root, g.name, ctx, nil, nil)
}
