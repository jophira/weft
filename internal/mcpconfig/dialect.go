package mcpconfig

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// Harness names, matching the names used elsewhere in weft.
const (
	HarnessClaudeCode = "claude-code"
	HarnessCodex      = "codex"
	HarnessCursor     = "cursor"
	HarnessGemini     = "gemini"
)

// Keys the MCP server table lives under in each native format.
const (
	jsonServersKey = "mcpServers"
	tomlServersKey = "mcp_servers"
)

// Dialect translates between the canonical Config and one harness's native
// MCP file.
//
// cf. Java: an interface with one implementation per vendor, selected from a
// registry. Go's implicit satisfaction means the implementations never name
// this interface.
type Dialect interface {
	// Name is the harness this dialect writes for.
	Name() string

	// Path is the absolute path of the native file. It is returned whether or
	// not the file exists — callers need the path to create it.
	Path() (string, error)

	// ToNative merges c into existing and returns the new document. Only the
	// dialect's MCP key is touched; every other key in existing survives
	// untouched. Pass nil or empty existing for a new file.
	ToNative(c Config, existing []byte) ([]byte, error)

	// ToCanonical reads a native document back into canonical form. It fails
	// when the document holds a literal credential, since adoption must not
	// copy one into a source.
	ToCanonical(native []byte) (Config, error)
}

// Dialects returns every supported dialect, ordered by harness name so callers
// that iterate produce stable output.
func Dialects() []Dialect {
	return []Dialect{
		&jsonDialect{harness: HarnessClaudeCode, rel: []string{".claude.json"}, codec: typedCodec{}},
		&tomlDialect{harness: HarnessCodex, rel: []string{".codex", "config.toml"}, codec: typedCodec{}},
		&jsonDialect{harness: HarnessCursor, rel: []string{".cursor", "mcp.json"}, codec: typedCodec{}},
		&jsonDialect{harness: HarnessGemini, rel: []string{".gemini", "settings.json"}, codec: geminiCodec{}},
	}
}

// DialectFor returns the dialect for a harness name, or false when the harness
// has no MCP support.
func DialectFor(harness string) (Dialect, bool) {
	for _, d := range Dialects() {
		if d.Name() == harness {
			return d, true
		}
	}
	return nil, false
}

// homePath resolves a home-relative native file path.
func homePath(rel ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(append([]string{home}, rel...)...), nil
}

// serverCodec encodes and decodes a single server entry. The wire container
// (JSON object, TOML table) is the dialect's business; the codec only owns
// which keys a harness uses inside one server entry.
type serverCodec interface {
	encode(s Server) map[string]any
	decode(name string, raw map[string]any) (Server, error)
}

// typedCodec is the shape Claude Code, Cursor and Codex share: stdio servers
// are described by command/args/env, remote servers by an explicit type plus
// url/headers.
//
// "type" is omitted for stdio because that is the documented default in all
// three tools and their own files leave it out; Normalize puts it back on the
// way in, so the round trip still holds.
type typedCodec struct{}

func (typedCodec) encode(s Server) map[string]any {
	out := map[string]any{}
	switch s.Type {
	case TypeHTTP, TypeSSE:
		out["type"] = s.Type
		out["url"] = s.URL
		putStringMap(out, "headers", s.Headers)
	default:
		out["command"] = s.Command
		putStringSlice(out, "args", s.Args)
		putStringMap(out, "env", s.Env)
	}
	return out
}

func (typedCodec) decode(name string, raw map[string]any) (Server, error) {
	s := Server{Name: name}
	var err error
	if s.Type, err = stringField(name, raw, "type"); err != nil {
		return Server{}, err
	}
	if s.Type == "" {
		s.Type = TypeStdio
	}
	if s.Command, err = stringField(name, raw, "command"); err != nil {
		return Server{}, err
	}
	if s.URL, err = stringField(name, raw, "url"); err != nil {
		return Server{}, err
	}
	if s.Args, err = stringSliceField(name, raw, "args"); err != nil {
		return Server{}, err
	}
	if s.Env, err = stringMapField(name, raw, "env"); err != nil {
		return Server{}, err
	}
	if s.Headers, err = stringMapField(name, raw, "headers"); err != nil {
		return Server{}, err
	}
	return s, nil
}

// geminiCodec follows Gemini CLI's settings.json, which encodes the transport
// in the key name rather than a "type" field: "httpUrl" for streamable HTTP and
// "url" for SSE. Emitting Claude's shape here would produce a file Gemini
// silently ignores, which is exactly the class of bug ADR 0004 exists to fix.
type geminiCodec struct{}

func (geminiCodec) encode(s Server) map[string]any {
	out := map[string]any{}
	switch s.Type {
	case TypeHTTP:
		out["httpUrl"] = s.URL
		putStringMap(out, "headers", s.Headers)
	case TypeSSE:
		out["url"] = s.URL
		putStringMap(out, "headers", s.Headers)
	default:
		out["command"] = s.Command
		putStringSlice(out, "args", s.Args)
		putStringMap(out, "env", s.Env)
	}
	return out
}

func (geminiCodec) decode(name string, raw map[string]any) (Server, error) {
	s := Server{Name: name}
	httpURL, err := stringField(name, raw, "httpUrl")
	if err != nil {
		return Server{}, err
	}
	sseURL, err := stringField(name, raw, "url")
	if err != nil {
		return Server{}, err
	}
	switch {
	case httpURL != "":
		s.Type, s.URL = TypeHTTP, httpURL
	case sseURL != "":
		s.Type, s.URL = TypeSSE, sseURL
	default:
		s.Type = TypeStdio
	}
	if s.Command, err = stringField(name, raw, "command"); err != nil {
		return Server{}, err
	}
	if s.Args, err = stringSliceField(name, raw, "args"); err != nil {
		return Server{}, err
	}
	if s.Env, err = stringMapField(name, raw, "env"); err != nil {
		return Server{}, err
	}
	if s.Headers, err = stringMapField(name, raw, "headers"); err != nil {
		return Server{}, err
	}
	return s, nil
}

// encodeServers turns the canonical server map into the generic map the
// concrete formats marshal. Returns nil when there is nothing to write, which
// tells the dialect to drop its key rather than write an empty table.
func encodeServers(c Config, codec serverCodec) map[string]any {
	if len(c.Servers) == 0 {
		return nil
	}
	out := make(map[string]any, len(c.Servers))
	for name, s := range c.Servers {
		out[name] = codec.encode(s)
	}
	return out
}

// decodeServers is the inverse of encodeServers. Server names are walked in
// sorted order so a document with two bad entries always reports the same one.
func decodeServers(raw map[string]any, codec serverCodec) (Config, error) {
	if len(raw) == 0 {
		return Config{}, nil
	}
	servers := make(map[string]Server, len(raw))
	for _, name := range slices.Sorted(maps.Keys(raw)) {
		entry, ok := raw[name].(map[string]any)
		if !ok {
			return Config{}, fmt.Errorf("mcp server %q: expected a table of settings, got %T", name, raw[name])
		}
		s, err := codec.decode(name, entry)
		if err != nil {
			return Config{}, err
		}
		servers[name] = s
	}
	c := Config{Servers: servers}.Normalize()
	// Validate here rather than in the caller: ToCanonical is the adoption
	// boundary, and a literal credential must be refused before it can reach a
	// source directory that may have a git remote.
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// putStringSlice and putStringMap omit empty collections so native files stay
// free of noise keys like "args": [], which the harnesses never write either.
func putStringSlice(m map[string]any, key string, v []string) {
	if len(v) == 0 {
		return
	}
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	m[key] = out
}

func putStringMap(m map[string]any, key string, v map[string]string) {
	if len(v) == 0 {
		return
	}
	out := make(map[string]any, len(v))
	for k, s := range v {
		out[k] = s
	}
	m[key] = out
}

// stringField reads an optional string. A present-but-wrong-typed value is an
// error rather than a silent zero: failing closed on an unknown shape is ADR
// 0004's stated response to vendor churn.
func stringField(server string, raw map[string]any, key string) (string, error) {
	v, ok := raw[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("mcp server %q: %s must be a string, got %T", server, key, v)
	}
	return s, nil
}

func stringSliceField(server string, raw map[string]any, key string) ([]string, error) {
	v, ok := raw[key]
	if !ok || v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("mcp server %q: %s must be a list, got %T", server, key, v)
	}
	out := make([]string, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("mcp server %q: %s entry %d must be a string, got %T", server, key, i, item)
		}
		out[i] = s
	}
	return out, nil
}

func stringMapField(server string, raw map[string]any, key string) (map[string]string, error) {
	v, ok := raw[key]
	if !ok || v == nil {
		return nil, nil
	}
	entries, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcp server %q: %s must be a table, got %T", server, key, v)
	}
	out := make(map[string]string, len(entries))
	for k, item := range entries {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("mcp server %q: %s value %q must be a string, got %T", server, key, k, item)
		}
		out[k] = s
	}
	return out, nil
}
