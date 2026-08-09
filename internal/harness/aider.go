package harness

import (
	"github.com/jophira/weft/internal/locate"
)

// Aider adapts Weft to aider's conventions file.
//
// Aider has no standard global conventions path, so weft writes
// ~/.aider/CONVENTIONS.md — ~/.aider is aider's own state directory (analytics,
// install records, caches), not one weft invents.
//
// Aider does not read that file on its own. It has no "conventions" option; the
// mechanism is the general read-only file flag, so wiring it up means adding
// `read: ~/.aider/CONVENTIONS.md` to ~/.aider.conf.yml. Weft does not yet write
// that entry, so an applied profile is inert until the user adds it by hand.
type Aider struct{ detection }

func (a *Aider) Name() string { return "aider" }

// detectSignals probes aider's state directory and its optional config file, as
// well as the binary. ~/.aider.conf.yml is not created by default, so the
// directory is the signal that actually fires on a stock install.
func (a *Aider) detectSignals() detectSpec {
	return detectSpec{
		binary: "aider",
		candidates: []locate.Candidate{
			locate.HomeRel(".aider"),
			locate.HomeRel(".aider.conf.yml"),
		},
	}
}

func (a *Aider) Detect() bool { return a.run(a.detectSignals()) }

// Apply copies files from stagedRoot into ~/.aider/, renaming CLAUDE.md → CONVENTIONS.md.
func (a *Aider) Apply(stagedRoot string, ctx ApplyCtx) error {
	return applyToHomeDir(stagedRoot, ".aider", a, ctx, map[string]string{
		"CLAUDE.md": "CONVENTIONS.md",
	})
}

// ClassSupport: aider reads one conventions file and has no commands, agents or
// skills — everything else is advertised.
func (a *Aider) ClassSupport(cl Class) ClassSupport {
	switch cl {
	case ClassInstructions:
		return ClassSupport{Placement: PlacementInstruction}
	case ClassCommands, ClassAgents, ClassSkills:
		return ClassSupport{Placement: PlacementNone, Advertise: true}
	default:
		return ClassSupport{Placement: PlacementNone}
	}
}

// InstructionSpec: aider reads a single conventions file, so weft inlines
// content within a managed block (Tier B).
func (a *Aider) InstructionSpec() (InstructionSpec, error) {
	path, err := homeJoin(".aider", "CONVENTIONS.md")
	return InstructionSpec{Path: path, Strategy: StrategyInline}, err
}
