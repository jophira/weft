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

// KnownFiles: Claude Code has the widest surface of any harness weft targets,
// and most of it is not projected. Declaring the whole set is what turns
// "does weft handle all of it" into a report.
func (c *ClaudeCode) KnownFiles() []KnownFile {
	return []KnownFile{
		{Rel: "CLAUDE.md", Kind: FileInstructions, Desc: "root instruction file"},
		{Rel: "settings.json", Kind: FileSettings, Desc: "hooks, permissions, env, status line"},
		{Rel: "settings.local.json", Kind: FileSettings, Desc: "machine-local settings overrides"},
		{Rel: "keybindings.json", Kind: FileKeybindings, Desc: "custom key bindings"},
		{Rel: "statusline-command.sh", Kind: FileStatusline, Desc: "status line command"},
		{Rel: "commands", Dir: true, Kind: FileCommands, Desc: "slash commands"},
		{Rel: "agents", Dir: true, Kind: FileAgents, Desc: "subagent definitions"},
		{Rel: "skills", Dir: true, Kind: FileSkills, Desc: "skill bundles"},
		{Rel: "hooks", Dir: true, Kind: FileHooks, Desc: "hook scripts"},
		{Rel: "output-styles", Dir: true, Kind: FileOutputStyles, Desc: "response style presets"},
		{Rel: "plugins", Dir: true, Kind: FilePlugins, Desc: "installed plugins"},
	}
}

// StateEntries: everything Claude Code writes for itself. Session transcripts
// alone run to thousands of files, so leaving them in the unrecognised count
// would bury the handful of entries worth acting on.
func (c *ClaudeCode) StateEntries() []string {
	return []string{
		"projects", "sessions", "history.jsonl", "todos", "statsig", "cache",
		"debug", "downloads", "file-history", "ide", "jobs", "paste-cache",
		"plans", "session-env", "shell-snapshots", "tasks", "telemetry",
		"usage-data", "backups", "chrome", "daemon", "daemon.log", ".cc-writes",
		"tmp", ".last-cleanup", ".last-update-result.json",
		// Backups weft and the harness both leave behind, matched by shape so the
		// list does not need a new entry every time one is written.
		"*.bak", "*.bak-*", "*.log",
	}
}
