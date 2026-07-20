package mcpconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// jsonIndent matches what Claude Code, Cursor and Gemini CLI write themselves,
// so weft's rewrite does not reformat the whole file into a spurious diff.
const jsonIndent = "  "

// jsonDialect serves every harness that keeps MCP servers under an "mcpServers"
// object in a JSON file. The harnesses differ only in path and in the keys used
// inside one server entry, both injected here.
type jsonDialect struct {
	harness string
	rel     []string // path segments below the user's home directory
	codec   serverCodec
}

func (d *jsonDialect) Name() string { return d.harness }

func (d *jsonDialect) Path() (string, error) { return homePath(d.rel...) }

// ToNative replaces the mcpServers key in existing and returns the whole
// document.
//
// The merge is the point. ~/.claude.json also holds project history, onboarding
// flags and other local state that only Claude Code understands; regenerating
// the file from the canonical model would destroy all of it. Unrelated keys are
// carried through as decoded values and re-encoded untouched.
func (d *jsonDialect) ToNative(c Config, existing []byte) ([]byte, error) {
	doc, err := d.decodeDoc(existing)
	if err != nil {
		return nil, err
	}
	if servers := encodeServers(c, d.codec); servers != nil {
		doc[jsonServersKey] = servers
	} else {
		// An empty canonical config means "weft manages no servers here", which
		// is expressed by dropping the key rather than leaving "mcpServers": {}.
		delete(doc, jsonServersKey)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", jsonIndent)
	// Go escapes &, < and > for HTML embedding by default. These files are read
	// by CLI tools, and escaping would mangle query strings in server URLs.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("serialising %s mcp config: %w", d.harness, err)
	}
	return buf.Bytes(), nil
}

func (d *jsonDialect) ToCanonical(native []byte) (Config, error) {
	doc, err := d.decodeDoc(native)
	if err != nil {
		return Config{}, err
	}
	entry, ok := doc[jsonServersKey]
	if !ok || entry == nil {
		return Config{}, nil
	}
	raw, ok := entry.(map[string]any)
	if !ok {
		return Config{}, fmt.Errorf("%s mcp config: %s must be an object, got %T", d.harness, jsonServersKey, entry)
	}
	return decodeServers(raw, d.codec)
}

// decodeDoc parses a native JSON document, treating absent or blank input as an
// empty document so a first-run write does not need a special case.
//
// UseNumber keeps numeric literals as their original text. Without it every
// number would round-trip through float64 and an unrelated key holding a large
// integer id could come back rewritten in scientific notation — data loss in a
// file weft does not own.
func (d *jsonDialect) decodeDoc(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing %s mcp config: %w", d.harness, err)
	}
	if doc == nil {
		return map[string]any{}, nil
	}
	return doc, nil
}
