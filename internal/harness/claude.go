package harness

import (
	"github.com/jophira/weft/internal/locate"
)

// ClaudeCode adapts Weft to Claude Code's ~/.claude layout.
type ClaudeCode struct{ detection }

func (c *ClaudeCode) Name() string { return "claude-code" }

func (c *ClaudeCode) detectSignals() detectSpec {
	return detectSpec{binaries: []string{"claude"}, candidates: []locate.Candidate{locate.HomeRel(".claude")}}
}

func (c *ClaudeCode) Detect() bool { return c.run(c.detectSignals()) }

// Apply copies every file from stagedRoot into ~/.claude/, creating
// subdirectories as needed. Existing files owned by weft are overwritten
// silently; externally-modified files are backed up first.
func (c *ClaudeCode) Apply(stagedRoot string, ctx ApplyCtx) error {
	return applyToHomeDir(stagedRoot, ".claude", c, ctx, nil)
}

// ClassSupport: Claude Code is weft's reference layout — every class has a native
// home at the same path the staged tree uses.
func (c *ClaudeCode) ClassSupport(cl Class) ClassSupport {
	switch cl {
	case ClassInstructions:
		return ClassSupport{Placement: PlacementInstruction}
	case ClassCommands:
		return ClassSupport{Placement: PlacementNative, SubDir: "commands"}
	case ClassAgents:
		return ClassSupport{Placement: PlacementNative, SubDir: "agents"}
	case ClassSkills:
		return ClassSupport{Placement: PlacementNative, SubDir: "skills"}
	case ClassMCP:
		// Wired in D4; until the canonical emitter lands, weft does not touch
		// ~/.claude.json — it holds unrelated local state.
		return ClassSupport{Placement: PlacementNone}
	default:
		return ClassSupport{Placement: PlacementNative}
	}
}

// InstructionSpec: Claude Code follows @-imports in ~/.claude/CLAUDE.md, so weft
// keeps content in its own copies and imports them (Tier A).
func (c *ClaudeCode) InstructionSpec() (InstructionSpec, error) {
	path, err := homeJoin(".claude", "CLAUDE.md")
	return InstructionSpec{Path: path, Strategy: StrategyImport, ImportTemplate: "@{path}"}, err
}

// ProjectSpec: Claude Code runs SessionStart hooks and reads project settings
// from .claude/settings.local.json, which is the conventionally gitignored one
// of the pair. That combination is the only delivery costing no tracked diff at
// all, so Claude Code takes the hook and its project CLAUDE.md is left alone
// even though @-imports would work there too.
func (c *ClaudeCode) ProjectSpec() ProjectSpec {
	return ProjectSpec{
		Delivery: ProjectHook,
		Path:     ".claude/settings.local.json",
		Inputs:   []string{"CLAUDE.md", ".claude/CLAUDE.md"},
	}
}
