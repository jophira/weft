package harness

import (
	"fmt"
	"os"

	"github.com/jophira/weft/internal/locate"
)

// Aider adapts Weft to aider's conventions file.
//
// Aider has no standard global conventions path, so weft writes
// ~/.aider/CONVENTIONS.md — ~/.aider is aider's own state directory (analytics,
// install records, caches), not one weft invents.
//
// Aider does not read that file on its own. It has no "conventions" option; the
// mechanism is the general read-only file flag, so Wire adds a `read` entry
// pointing at it to ~/.aider.conf.yml.
//
// Aider has no MCP support, so there is deliberately no mcpconfig dialect for it
// and ProjectMCP no-ops. That silence is a decision, not an omission.
type Aider struct{ detection }

func (a *Aider) Name() string { return "aider" }

// detectSignals probes aider's state directory and its optional config file, as
// well as the binary. ~/.aider.conf.yml is not created by default, so the
// directory is the signal that actually fires on a stock install.
func (a *Aider) detectSignals() detectSpec {
	return detectSpec{
		binaries: []string{"aider"},
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

// Wire implements Wirer: it points aider at the conventions file weft wrote.
//
// The config is a document the user owns, so this merges a `read` entry into it
// through the same tracked-sidecar path the MCP projection uses, backing up
// anything changed outside weft rather than overwriting it.
//
// Aider searches for .aider.conf.yml in the git root, the working directory and
// the home directory, and does not merge them. Weft manages the home copy, so a
// project-level config in a repo takes precedence over this entry.
func (a *Aider) Wire(ctx ApplyCtx) error {
	spec, err := a.InstructionSpec()
	if err != nil {
		return err
	}
	confPath, err := homeJoin(".aider.conf.yml")
	if err != nil {
		return err
	}

	existing, err := os.ReadFile(confPath) //nolint:gosec // path is derived from the home dir, not user input
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", confPath, err)
	}

	// The absolute path is written deliberately. A leading ~ depends on aider
	// expanding it, which its config loader does not promise, and this file is
	// machine-local state weft regenerates on every apply.
	merged, err := mergeAiderRead(existing, spec.Path)
	if err != nil {
		return fmt.Errorf("wiring %s: %w", confPath, err)
	}

	return writeTrackedSidecar(confPath, wiringManifestKey(confPath), a.Name(), merged, ctx)
}

// KnownFiles: aider's conventions file lives in the config root, but the config
// pointing at it sits at ~/.aider.conf.yml, outside this root. The report names
// only what is inside.
func (a *Aider) KnownFiles() []KnownFile {
	return []KnownFile{
		{Rel: "CONVENTIONS.md", Kind: FileInstructions, Desc: "coding conventions"},
	}
}

func (a *Aider) StateEntries() []string {
	return []string{
		"caches", "analytics.jsonl", "analytics.json", "installs.json",
		".aider.chat.history.md", "tmp",
	}
}
