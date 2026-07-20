package harness

import (
	"os"
	"path/filepath"
)

// Windsurf adapts Weft to Windsurf's global rules layout.
// Global rules live at ~/.codeium/windsurf/global_rules.md.
type Windsurf struct{}

func (w *Windsurf) Name() string { return "windsurf" }

func (w *Windsurf) Detect() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".codeium", "windsurf"))
	return err == nil
}

// Apply copies files from stagedRoot into ~/.codeium/windsurf/,
// renaming CLAUDE.md → global_rules.md.
func (w *Windsurf) Apply(stagedRoot string, ctx ApplyCtx) error {
	return applyToHomeDir(stagedRoot, filepath.Join(".codeium", "windsurf"), w, ctx, map[string]string{
		"CLAUDE.md": "global_rules.md",
	})
}

// ClassSupport: Windsurf consumes exactly one global rules file and has no
// commands, agents or skills of its own — everything else is advertised.
func (w *Windsurf) ClassSupport(cl Class) ClassSupport {
	switch cl {
	case ClassInstructions:
		return ClassSupport{Placement: PlacementInstruction}
	case ClassCommands, ClassAgents, ClassSkills:
		return ClassSupport{Placement: PlacementNone, Advertise: true}
	default:
		return ClassSupport{Placement: PlacementNone}
	}
}

// InstructionSpec: Windsurf reads a single global_rules.md, so weft inlines
// content within a managed block (Tier B).
func (w *Windsurf) InstructionSpec() (InstructionSpec, error) {
	path, err := homeJoin(".codeium", "windsurf", "global_rules.md")
	return InstructionSpec{Path: path, Strategy: StrategyInline}, err
}
