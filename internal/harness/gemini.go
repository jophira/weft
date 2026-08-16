package harness

import (
	"github.com/jophira/weft/internal/locate"
)

// GeminiCLI adapts Weft to Gemini CLI's ~/.gemini layout.
// Gemini CLI reads GEMINI.md rather than CLAUDE.md.
type GeminiCLI struct{ detection }

func (g *GeminiCLI) Name() string { return "gemini-cli" }

func (g *GeminiCLI) detectSignals() detectSpec {
	return detectSpec{binaries: []string{"gemini"}, candidates: []locate.Candidate{locate.HomeRel(".gemini")}}
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

// ProjectSpec: Gemini CLI has no session-start command hook, but it does resolve
// @-imports inside a project GEMINI.md, so one line pointing into <repo>/.weft
// is enough and never needs rewriting.
//
// The import target sits inside the repository deliberately. Gemini's import
// processor validates paths against an allowed-directory list, so a pointer
// reaching into $HOME could be refused; one staying within the project cannot.
func (g *GeminiCLI) ProjectSpec() ProjectSpec {
	return ProjectSpec{
		Delivery:       ProjectImport,
		Path:           "GEMINI.md",
		ImportTemplate: "@{path}",
		Inputs:         []string{"GEMINI.md"},
	}
}

// KnownFiles: Gemini CLI keeps servers in settings.json alongside everything
// else, so that one file is both settings and MCP.
func (g *GeminiCLI) KnownFiles() []KnownFile {
	return []KnownFile{
		{Rel: "GEMINI.md", Kind: FileInstructions, Desc: "root instruction file"},
		{Rel: "settings.json", Kind: FileSettings, Desc: "settings and MCP servers"},
		{Rel: "commands", Dir: true, Kind: FileCommands, Desc: "custom commands"},
	}
}

func (g *GeminiCLI) StateEntries() []string {
	return []string{"tmp", "antigravity", "history", "logs", "installation_id"}
}
