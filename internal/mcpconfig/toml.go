package mcpconfig

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// tomlDialect serves Codex, which keeps MCP servers as [mcp_servers.<name>]
// tables inside ~/.codex/config.toml alongside unrelated settings.
type tomlDialect struct {
	harness string
	rel     []string // path segments below the user's home directory
	codec   serverCodec
}

func (d *tomlDialect) Name() string { return d.harness }

func (d *tomlDialect) Path() (string, error) { return homePath(d.rel...) }

// ToNative rewrites only the mcp_servers tables and leaves the rest of the file
// byte-for-byte intact.
//
// This is done textually rather than by decoding the document and re-encoding
// it. config.toml is hand-edited and routinely carries comments and a
// deliberate key order, and neither survives a map round trip through any Go
// TOML encoder — losing them would be exactly the silent data loss ADR 0004
// rules out. The document is still parsed first, so a malformed file is
// reported instead of being spliced blindly.
func (d *tomlDialect) ToNative(c Config, existing []byte) ([]byte, error) {
	if _, err := d.parseDoc(existing); err != nil {
		return nil, err
	}
	kept := stripTOMLTable(existing, tomlServersKey)

	servers := encodeServers(c, d.codec)
	if servers == nil {
		return kept, nil
	}
	rendered, err := toml.Marshal(map[string]any{tomlServersKey: servers})
	if err != nil {
		return nil, fmt.Errorf("serialising %s mcp config: %w", d.harness, err)
	}
	if len(kept) == 0 {
		return rendered, nil
	}
	// One blank line between the preserved body and the regenerated tables.
	// Trimming first keeps repeated applies byte-stable: without it the strip
	// would leave the separator behind and each apply would add another.
	return append(append(kept, "\n\n"...), rendered...), nil
}

func (d *tomlDialect) ToCanonical(native []byte) (Config, error) {
	doc, err := d.parseDoc(native)
	if err != nil {
		return Config{}, err
	}
	entry, ok := doc[tomlServersKey]
	if !ok || entry == nil {
		return Config{}, nil
	}
	raw, ok := entry.(map[string]any)
	if !ok {
		return Config{}, fmt.Errorf("%s mcp config: %s must be a table, got %T", d.harness, tomlServersKey, entry)
	}
	return decodeServers(raw, d.codec)
}

// parseDoc decodes a TOML document, treating blank input as an empty one.
// Always returns a usable map so callers never nil-check the result.
func (d *tomlDialect) parseDoc(data []byte) (map[string]any, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return doc, nil
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s mcp config: %w", d.harness, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// tomlTableHeader matches a table or array-of-tables header line and captures
// the dotted key inside the brackets.
var tomlTableHeader = regexp.MustCompile(`^\s*\[\[?\s*([^\[\]]*?)\s*\]\]?`)

// stripTOMLTable removes every line belonging to the root table and its
// sub-tables, and returns the remainder with trailing blank lines trimmed.
//
// A table's extent runs from its header to the next header at any level, which
// is what makes this tractable line by line: nothing else in TOML opens a
// bracket at the start of a line.
func stripTOMLTable(doc []byte, root string) []byte {
	if len(bytes.TrimSpace(doc)) == 0 {
		return nil
	}
	var kept []string
	dropping, inMultiline := false, false
	for _, line := range strings.Split(string(doc), "\n") {
		if !inMultiline {
			if m := tomlTableHeader.FindStringSubmatch(line); m != nil {
				dropping = tomlRootKey(m[1]) == root
			}
		}
		if !dropping {
			kept = append(kept, line)
		}
		inMultiline = togglesMultiline(line, inMultiline)
	}
	return []byte(strings.TrimRight(strings.Join(kept, "\n"), "\n\t "))
}

// tomlRootKey returns the first segment of a dotted table key, unquoted.
func tomlRootKey(key string) string {
	first, _, _ := strings.Cut(key, ".")
	return strings.Trim(strings.TrimSpace(first), `"'`)
}

// togglesMultiline tracks whether the scanner is inside a multi-line string, so
// a line such as `[not a header]` embedded in one is not mistaken for a table
// header. It counts delimiters per line, which is sufficient for real config
// files; a single line opening one kind of multi-line string while a delimiter
// of the other kind appears inside it would confuse it, and no TOML weft writes
// or reads does that.
func togglesMultiline(line string, in bool) bool {
	if strings.Count(line, `"""`)%2 == 1 {
		in = !in
	}
	if strings.Count(line, `'''`)%2 == 1 {
		in = !in
	}
	return in
}
