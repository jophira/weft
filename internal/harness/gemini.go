package harness

import (
	"github.com/jophira/weft/internal/locate"
)

// GeminiCLI adapts Weft to Gemini CLI's ~/.gemini layout.
// Gemini CLI reads GEMINI.md rather than CLAUDE.md.
type GeminiCLI struct{ detection }

func (g *GeminiCLI) Name() string { return "gemini-cli" }

func (g *GeminiCLI) detectSignals() detectSpec {
	return detectSpec{binary: "gemini", candidates: []locate.Candidate{locate.HomeRel(".gemini")}}
}

func (g *GeminiCLI) Detect() bool { return g.run(g.detectSignals()) }

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
