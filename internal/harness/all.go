package harness

import "github.com/jophira/weft/internal/locate"

// Known pairs a Harness with a fallback display string for the CONFIG column.
// When the Harness also implements ConfigPather, that takes precedence.
type Known struct {
	H          Harness
	ConfigPath string // static fallback; use "" when the harness implements ConfigPather
}

// builtins returns the compile-time harness list.
// Typed structs (ClaudeCode, Warp, …) carry custom apply logic.
// GenericHarness handles plain directory-copy tools.
func builtins() []Known {
	return []Known{
		// ── Typed harnesses ──────────────────────────────────────────────────
		{&ClaudeCode{}, "~/.claude"},
		{&Cursor{}, "~/.cursor"},
		{&Warp{}, ""}, // ConfigPath via ConfigPather
		{&Aider{}, "~/.aider.conf.yml  or  aider in PATH"},

		// ── Generic harnesses (plain directory copy) ─────────────────────────
		// ConfigPath is resolved at runtime via ConfigPather; static field is "".
		{
			&GenericHarness{
				name:         "codex",
				detectBinary: "codex",
				candidates:   []locate.Candidate{locate.HomeRel(".codex")},
			},
			"",
		},
		{
			&GenericHarness{
				name:       "antigravity",
				candidates: []locate.Candidate{locate.HomeRel(".gemini", "antigravity")},
			},
			"",
		},
		{
			&GenericHarness{
				name:         "gemini-cli",
				detectBinary: "gemini",
				candidates:   []locate.Candidate{locate.HomeRel(".gemini")},
			},
			"",
		},
		{
			&GenericHarness{
				name:         "opencode",
				detectBinary: "opencode",
				candidates:   []locate.Candidate{locate.XDGRel("opencode")},
			},
			"",
		},
		{
			&GenericHarness{
				name:         "hermes",
				detectBinary: "hermes",
				candidates:   []locate.Candidate{locate.HomeRel(".hermes")},
			},
			"",
		},
		{
			&GenericHarness{
				name:       "windsurf",
				candidates: []locate.Candidate{locate.HomeRel(".codeium", "windsurf")},
			},
			"",
		},
		{
			&GenericHarness{
				name:         "goose",
				detectBinary: "goose",
				candidates:   []locate.Candidate{locate.XDGRel("goose")},
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
