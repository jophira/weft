package harness

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jophira/weft/internal/locate"
)

// GenericHarness handles tools whose apply strategy is a plain directory copy.
// Most AI coding tools fall into this category — detect a config root or binary,
// then copy the staged output into the resolved directory.
type GenericHarness struct {
	detection
	name           string
	detectBinaries []string           // binary names looked up via PATH, in order; empty = skip
	candidates     []locate.Candidate // config root candidates; probed in order
	// known and state feed the coverage report. Both are optional: a generic
	// harness weft knows nothing about reports an undeclared layout, which is
	// honest, rather than an empty one, which would read as "nothing here".
	known []KnownFile
	state []string
}

// KnownFiles implements FileAware for the generic adapters that declare a layout.
func (g *GenericHarness) KnownFiles() []KnownFile { return g.known }

// StateEntries implements FileAware.
func (g *GenericHarness) StateEntries() []string { return g.state }

func (g *GenericHarness) Name() string { return g.name }

func (g *GenericHarness) detectSignals() detectSpec {
	return detectSpec{binaries: g.detectBinaries, candidates: g.candidates}
}

func (g *GenericHarness) Detect() bool { return g.run(g.detectSignals()) }

// ConfigPath implements ConfigPather: returns the resolved root when detected,
// or the full candidate display string otherwise.
func (g *GenericHarness) ConfigPath() string {
	if root := g.detectedRoot(); root != "" {
		return locate.Tilde(root)
	}
	return locate.Display(g.candidates)
}

// InstructionSpec: directory-copy harnesses have no known include directive, so
// weft inlines content (Tier B) into <root>/CLAUDE.md. The default for any
// unknown or user-defined harness — the safe, fool-proof fallback.
func (g *GenericHarness) InstructionSpec() (InstructionSpec, error) {
	root, err := g.requireRoot()
	if err != nil {
		return InstructionSpec{}, err
	}
	return InstructionSpec{Path: filepath.Join(root, "CLAUDE.md"), Strategy: StrategyInline}, nil
}

func (g *GenericHarness) Apply(stagedRoot string, ctx ApplyCtx) error {
	root, err := g.requireRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("ensuring %s exists: %w", root, err)
	}
	return applyWithManifest(stagedRoot, root, g.name, ctx, nil, nil, g)
}

// requireRoot returns the resolved config root, probing once if Detect has not
// run yet.
func (g *GenericHarness) requireRoot() (string, error) {
	if root := g.detectedRoot(); root != "" {
		return root, nil
	}
	if g.Detect() {
		return g.detectedRoot(), nil
	}
	return "", fmt.Errorf("%s not detected — install it or create its config directory", g.name)
}
