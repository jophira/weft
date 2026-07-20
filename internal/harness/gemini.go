package harness

import (
	"os"
	"os/exec"
	"path/filepath"
)

// GeminiCLI adapts Weft to Gemini CLI's ~/.gemini layout.
// Gemini CLI reads GEMINI.md rather than CLAUDE.md.
type GeminiCLI struct{}

func (g *GeminiCLI) Name() string { return "gemini-cli" }

func (g *GeminiCLI) Detect() bool {
	if _, err := exec.LookPath("gemini"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".gemini"))
	return err == nil
}

// Apply copies files from stagedRoot into ~/.gemini/, renaming CLAUDE.md → GEMINI.md.
func (g *GeminiCLI) Apply(stagedRoot string, ctx ApplyCtx) error {
	return applyToHomeDir(stagedRoot, ".gemini", g, ctx, map[string]string{
		"CLAUDE.md": "GEMINI.md",
	})
}

// ClassSupport: Gemini CLI's custom commands are TOML files in ~/.gemini/commands/,
// not markdown, so relocating weft's .md commands there would produce files Gemini
// cannot parse. That is a format gap, not just a path gap — commands are advertised
// rather than translated until a TOML emitter exists.
func (g *GeminiCLI) ClassSupport(cl Class) ClassSupport {
	switch cl {
	case ClassInstructions:
		return ClassSupport{Placement: PlacementInstruction}
	case ClassCommands, ClassAgents, ClassSkills:
		return ClassSupport{Placement: PlacementNone, Advertise: true}
	default:
		return ClassSupport{Placement: PlacementNone}
	}
}

// InstructionSpec: Gemini CLI supports @-imports in ~/.gemini/GEMINI.md (Tier A).
func (g *GeminiCLI) InstructionSpec() (InstructionSpec, error) {
	path, err := homeJoin(".gemini", "GEMINI.md")
	return InstructionSpec{Path: path, Strategy: StrategyImport, ImportTemplate: "@{path}"}, err
}
