package harness

import (
	"github.com/jophira/weft/internal/locate"
)

// Codex adapts Weft to OpenAI Codex's ~/.codex layout.
// Codex reads AGENTS.md rather than CLAUDE.md.
type Codex struct{ detection }

func (c *Codex) Name() string { return "codex" }

func (c *Codex) detectSignals() detectSpec {
	return detectSpec{binary: "codex", candidates: []locate.Candidate{locate.HomeRel(".codex")}}
}

func (c *Codex) Detect() bool { return c.run(c.detectSignals()) }

// Apply copies files from stagedRoot into ~/.codex/, renaming CLAUDE.md → AGENTS.md.
func (c *Codex) Apply(stagedRoot string, ctx ApplyCtx) error {
	return applyToHomeDir(stagedRoot, ".codex", c, ctx, map[string]string{
		"CLAUDE.md": "AGENTS.md",
	})
}

// ClassSupport: Codex executes custom prompts from ~/.codex/prompts/ as markdown,
// so commands translate by relocation alone. It has no subagent or skill concept,
// so those are advertised as a read-on-demand index instead (ADR 0004 D9) rather
// than copied to a directory Codex would ignore.
func (c *Codex) ClassSupport(cl Class) ClassSupport {
	switch cl {
	case ClassInstructions:
		return ClassSupport{Placement: PlacementInstruction}
	case ClassCommands:
		return ClassSupport{Placement: PlacementNative, SubDir: "prompts"}
	case ClassAgents, ClassSkills:
		return ClassSupport{Placement: PlacementNone, Advertise: true}
	default:
		// Includes ClassMCP: Codex keeps servers in config.toml, which needs the
		// canonical emitter (D4), not a file copy.
		return ClassSupport{Placement: PlacementNone}
	}
}

// InstructionSpec: Codex reads a single ~/.codex/AGENTS.md with no include
// directive, so weft inlines content within a managed block (Tier B).
func (c *Codex) InstructionSpec() (InstructionSpec, error) {
	path, err := homeJoin(".codex", "AGENTS.md")
	return InstructionSpec{Path: path, Strategy: StrategyInline}, err
}
