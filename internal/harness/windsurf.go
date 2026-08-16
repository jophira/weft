package harness

import (
	"path/filepath"

	"github.com/jophira/weft/internal/locate"
)

// Windsurf adapts Weft to Windsurf's global rules layout.
// Global rules live at ~/.codeium/windsurf/global_rules.md.
type Windsurf struct{ detection }

func (w *Windsurf) Name() string { return "windsurf" }

func (w *Windsurf) detectSignals() detectSpec {
	return detectSpec{
		binaries:   []string{"windsurf"},
		candidates: []locate.Candidate{locate.HomeRel(".codeium", "windsurf")},
	}
}

func (w *Windsurf) Detect() bool { return w.run(w.detectSignals()) }

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

// KnownFiles: Windsurf reads one global rules file.
func (w *Windsurf) KnownFiles() []KnownFile {
	return []KnownFile{
		{Rel: "global_rules.md", Kind: FileInstructions, Desc: "global rules"},
		{Rel: "memories", Dir: true, Kind: FileInstructions, Desc: "persisted memories"},
	}
}

func (w *Windsurf) StateEntries() []string { return []string{"logs", "cache", "tmp"} }
