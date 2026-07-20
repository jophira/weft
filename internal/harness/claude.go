package harness

import (
	"os"
	"path/filepath"
)

// ClaudeCode adapts Weft to Claude Code's ~/.claude layout.
type ClaudeCode struct{}

func (c *ClaudeCode) Name() string { return "claude-code" }

func (c *ClaudeCode) Detect() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".claude"))
	return err == nil
}

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
