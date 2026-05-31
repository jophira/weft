package harness

// Known pairs a Harness with a human-readable config location for display.
type Known struct {
	H          Harness
	ConfigPath string // display-only, always uses ~/
}

// builtins returns the compile-time harness list.
// Harnesses with custom apply logic use typed structs; everything else uses
// GenericHarness (plain directory copy).
func builtins() []Known {
	return []Known{
		// Typed — custom apply logic.
		{&ClaudeCode{}, "~/.claude"},
		{&Cursor{}, "~/.cursor"},
		{&Warp{}, "~/.warp"},
		{&Aider{}, "~/.aider.conf.yml  or  aider in PATH"},
		// Generic — directory copy only.
		{
			&GenericHarness{name: "codex", detectBinary: "codex", detectPath: ".codex", configDir: ".codex"},
			"~/.codex",
		},
		{
			&GenericHarness{name: "antigravity", detectPath: ".gemini/antigravity", configDir: ".gemini/antigravity"},
			"~/.gemini/antigravity",
		},
		{
			&GenericHarness{name: "gemini-cli", detectBinary: "gemini", detectPath: ".gemini", configDir: ".gemini"},
			"~/.gemini",
		},
		{
			&GenericHarness{name: "opencode", detectBinary: "opencode", detectPath: ".config/opencode", configDir: ".config/opencode"},
			"~/.config/opencode",
		},
		{
			&GenericHarness{name: "hermes", detectBinary: "hermes", detectPath: ".hermes", configDir: ".hermes"},
			"~/.hermes",
		},
		{
			&GenericHarness{name: "windsurf", detectPath: ".codeium/windsurf", configDir: ".codeium/windsurf"},
			"~/.codeium/windsurf",
		},
		{
			&GenericHarness{name: "goose", detectBinary: "goose", detectPath: ".config/goose", configDir: ".config/goose"},
			"~/.config/goose",
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
