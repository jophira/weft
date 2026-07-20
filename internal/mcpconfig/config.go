// Package mcpconfig owns weft's canonical representation of MCP (Model Context
// Protocol) server definitions and the per-harness dialects it is emitted into.
//
// Every harness stores MCP servers in its own file and format — Claude Code in
// ~/.claude.json, Codex in ~/.codex/config.toml, Cursor in ~/.cursor/mcp.json,
// Gemini CLI in ~/.gemini/settings.json. Weft keeps one canonical mcp.yaml per
// source and translates in both directions, so a server added in any one
// harness can reach the others (ADR 0004, D4).
//
// Two invariants shape the whole package:
//
//   - Keyed merge, never whole-document rewrite. The native files hold plenty of
//     unrelated state (project history, onboarding flags, model settings). A
//     dialect replaces only its MCP key and leaves every other byte alone.
//   - Secrets by reference only. Canonical env/header values must be
//     indirections such as ${env:GITHUB_TOKEN}; a literal credential is refused
//     rather than written into a source that may be pushed to a git remote.
//
// Note the deliberate name: internal/mcp is weft's *own* MCP server. This
// package is about syncing *harness* MCP configuration and is unrelated to it.
package mcpconfig

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Transport types a server may use. Stdio is the default when a canonical
// document omits "type", matching every harness's own default.
const (
	TypeStdio = "stdio"
	TypeHTTP  = "http"
	TypeSSE   = "sse"
)

// Server is one MCP server definition in weft's harness-neutral form.
//
// Which fields are meaningful depends on Type: stdio servers are launched as a
// subprocess (Command/Args/Env), http and sse servers are reached over the
// network (URL/Headers). Validate enforces that split.
//
// Name mirrors the key this server is stored under in Config.Servers. It is not
// serialised — the map key is the single source of truth — but it is populated
// on every value weft hands out so error messages can name the offending server
// without the caller threading the key through.
// cf. Java: a @JsonIgnore field back-populated after Map deserialisation.
type Server struct {
	Name    string            `yaml:"-"`
	Type    string            `yaml:"type,omitempty"`
	Command string            `yaml:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	URL     string            `yaml:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

// Config is the full canonical document — the contents of one mcp.yaml.
type Config struct {
	Servers map[string]Server `yaml:"servers,omitempty"`
}

// Normalize returns c in the single canonical shape every producer in this
// package agrees on: names back-filled, absent Type defaulted to stdio, and
// empty collections reduced to nil.
//
// The reduction matters because idempotency here is asserted with
// reflect.DeepEqual, which distinguishes an empty map from a nil one. Formats
// disagree about how they render "no env" — TOML drops the table, JSON may keep
// `{}` — so both sides are collapsed to nil before comparison rather than every
// dialect having to be careful.
//
// cf. Python: a __post_init__ on a dataclass that canonicalises defaults.
func (c Config) Normalize() Config {
	if len(c.Servers) == 0 {
		return Config{}
	}
	out := make(map[string]Server, len(c.Servers))
	for name, s := range c.Servers {
		s.Name = name
		if s.Type == "" {
			s.Type = TypeStdio
		}
		if len(s.Args) == 0 {
			s.Args = nil
		}
		if len(s.Env) == 0 {
			s.Env = nil
		}
		if len(s.Headers) == 0 {
			s.Headers = nil
		}
		out[name] = s
	}
	return Config{Servers: out}
}

// Load reads and parses a canonical mcp.yaml. The result is normalised and
// validated, so a Config obtained from Load is always safe to hand to a dialect.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading mcp config %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parsing mcp config %s: %w", path, err)
	}
	c = c.Normalize()
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save writes c as canonical YAML, validating first so a literal credential can
// never land in a source directory.
//
// Output is byte-stable for a given Config: yaml.v3 sorts mapping keys, so
// server names and env/header keys always emit in the same order. That is a
// requirement, not a nicety — unstable output would show up as a phantom diff
// on every apply and make the watcher rewrite files in a loop.
func Save(path string, c Config) error {
	c = c.Normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("serialising mcp config: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
