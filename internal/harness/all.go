package harness

import "github.com/jophira/weft/internal/locate"

// Known pairs a Harness with a fallback display string for the CONFIG column.
// When the Harness also implements ConfigPather, that takes precedence.
type Known struct {
	H          Harness
	ConfigPath string // static fallback; use "" when the harness implements ConfigPather
}

// builtins returns the compile-time harness list.
// Typed structs carry custom apply logic (including filename mapping).
// GenericHarness handles plain directory-copy tools with no known filename conventions.
func builtins() []Known {
	return []Known{
		// ── Typed harnesses (with filename mapping) ───────────────────────────
		{&ClaudeCode{}, "~/.claude"},
		{&Codex{}, "~/.codex"},
		{&Cursor{}, "~/.cursor/rules"},
		{&Windsurf{}, "~/.codeium/windsurf"},
		{&GeminiCLI{}, "~/.gemini"},
		{&Warp{}, ""}, // ConfigPath via ConfigPather
		{&Aider{}, "~/.aider"},

		// ── Generic harnesses (plain directory copy) ─────────────────────────
		// ConfigPath is resolved at runtime via ConfigPather; static field is "".
		{
			&GenericHarness{
				name:       "antigravity",
				candidates: []locate.Candidate{locate.HomeRel(".gemini", "antigravity")},
				known: []KnownFile{
					{Rel: "commands", Dir: true, Kind: FileCommands, Desc: "custom commands"},
					{Rel: "knowledge", Dir: true, Kind: FileInstructions, Desc: "knowledge entries"},
					{Rel: "mcp_config.json", Kind: FileMCP, Desc: "MCP servers"},
				},
				state: []string{
					"brain", "code_tracker", "context_state", "conversations",
					"html_artifacts", "implicit", "installation_id",
					"browserAllowlist.txt", "user_settings.pb",
				},
			},
			"",
		},
		{
			&GenericHarness{
				name:           "opencode",
				detectBinaries: []string{"opencode"},
				// The config root is created on first run, not at install time,
				// so a freshly installed opencode is only visible through its
				// data root. Config stays first: it is where weft writes.
				candidates: []locate.Candidate{
					locate.XDGRel("opencode"),
					locate.XDGDataRel("opencode"),
				},
				known: []KnownFile{
					{Rel: "AGENTS.md", Kind: FileInstructions, Desc: "root instruction file"},
					{Rel: "opencode.json", Kind: FileSettings, Desc: "settings and MCP servers"},
					{Rel: "opencode.jsonc", Kind: FileSettings, Desc: "settings and MCP servers"},
					{Rel: "commands", Dir: true, Kind: FileCommands, Desc: "custom commands"},
				},
				state: []string{"node_modules", "package.json", "package-lock.json", ".gitignore", "log", "cache"},
			},
			"",
		},
		{
			&GenericHarness{
				name:           "hermes",
				detectBinaries: []string{"hermes"},
				candidates:     []locate.Candidate{locate.HomeRel(".hermes")},
			},
			"",
		},
		{
			&GenericHarness{
				name:           "goose",
				detectBinaries: []string{"goose"},
				candidates:     []locate.Candidate{locate.XDGRel("goose")},
			},
			"",
		},
	}
}

// All returns every harness — built-ins first, then any user-defined entries
// from ~/.config/weft/harnesses.yaml. A missing or malformed config file is
// silently ignored so the CLI never fails to start.
func All() []Known {
	all := builtins()
	if extras, err := loadConfigHarnesses(); err == nil {
		all = append(all, extras...)
	}
	return all
}

// Instances returns just the Harness slice — use with NewRegistry.
func Instances() []Harness {
	all := All()
	h := make([]Harness, len(all))
	for i, k := range all {
		h[i] = k.H
	}
	return h
}
