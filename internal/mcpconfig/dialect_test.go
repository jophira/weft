package mcpconfig

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// roundTripCases are the canonical documents every dialect must survive
// unchanged. Idempotency is the invariant that keeps bidirectional sync from
// oscillating: a dialect that cannot reproduce its input would rewrite the
// native file on every apply and churn the watcher forever.
func roundTripCases() []struct {
	name string
	cfg  Config
} {
	return []struct {
		name string
		cfg  Config
	}{
		{
			name: "stdio with args and env",
			cfg: Config{Servers: map[string]Server{
				"github": {
					Type:    TypeStdio,
					Command: "npx",
					Args:    []string{"-y", "@modelcontextprotocol/server-github"},
					// nolint:gosec // G101 false positive: this is the indirection
					// form, which is precisely what the secret guard requires.
					Env: map[string]string{"GITHUB_TOKEN": "${env:GITHUB_TOKEN}"},
				},
			}},
		},
		{
			name: "stdio without args or env",
			cfg: Config{Servers: map[string]Server{
				"plain": {Type: TypeStdio, Command: "server"},
			}},
		},
		{
			name: "http with headers",
			cfg: Config{Servers: map[string]Server{
				"remote": {
					Type:    TypeHTTP,
					URL:     "https://example.com/mcp",
					Headers: map[string]string{"Authorization": "${env:MCP_TOKEN}"},
				},
			}},
		},
		{
			name: "sse transport",
			cfg: Config{Servers: map[string]Server{
				"streamed": {Type: TypeSSE, URL: "https://example.com/sse"},
			}},
		},
		{
			name: "multiple servers of mixed transport",
			cfg: Config{Servers: map[string]Server{
				"alpha": {Type: TypeStdio, Command: "a", Args: []string{"--flag"}},
				"beta":  {Type: TypeHTTP, URL: "https://b.example/mcp"},
				"gamma": {Type: TypeStdio, Command: "c", Env: map[string]string{"K": "${K}"}},
			}},
		},
		{
			name: "empty config",
			cfg:  Config{},
		},
		{
			name: "unicode and spaces in server names",
			cfg: Config{Servers: map[string]Server{
				"my server":   {Type: TypeStdio, Command: "x"},
				"服务器":         {Type: TypeStdio, Command: "y"},
				"emoji-🚀-srv": {Type: TypeStdio, Command: "z"},
			}},
		},
	}
}

func TestDialects_RoundTripIsIdentity(t *testing.T) {
	for _, d := range Dialects() {
		for _, tc := range roundTripCases() {
			t.Run(d.Name()+"/"+tc.name, func(t *testing.T) {
				want := tc.cfg.Normalize()

				native, err := d.ToNative(want, nil)
				if err != nil {
					t.Fatalf("ToNative: %v", err)
				}
				got, err := d.ToCanonical(native)
				if err != nil {
					t.Fatalf("ToCanonical: %v", err)
				}

				if !reflect.DeepEqual(got.Normalize(), want) {
					t.Errorf("round trip changed the config\n got: %+v\nwant: %+v\nnative:\n%s",
						got.Normalize(), want, native)
				}
			})
		}
	}
}

// Gemini encodes transport in the key name rather than a "type" field, so its
// native output must not carry Claude's shape — that would be a file Gemini
// silently ignores, the exact bug class ADR 0004 addresses.
func TestGeminiDialect_UsesHTTPURLKey(t *testing.T) {
	d, ok := DialectFor(HarnessGemini)
	if !ok {
		t.Fatal("gemini dialect not registered")
	}
	cfg := Config{Servers: map[string]Server{
		"remote": {Type: TypeHTTP, URL: "https://example.com/mcp"},
	}}.Normalize()

	native, err := d.ToNative(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(native, []byte("httpUrl")) {
		t.Errorf("gemini http server must use the httpUrl key:\n%s", native)
	}
}

// The keyed merge is the guard against destroying user state. ~/.claude.json
// holds project history and onboarding flags; settings.json holds unrelated
// settings. Rewriting the whole document from the canonical model would lose
// all of it.
func TestJSONDialects_PreserveUnrelatedKeys(t *testing.T) {
	existing := []byte(`{
  "numStartups": 42,
  "projects": {"/home/u/repo": {"lastUsed": "2026-01-01"}},
  "tipsHistory": ["a", "b"],
  "mcpServers": {"stale": {"command": "old"}},
  "theme": "dark"
}`)

	cfg := Config{Servers: map[string]Server{
		"fresh": {Type: TypeStdio, Command: "new"},
	}}.Normalize()

	for _, name := range []string{HarnessClaudeCode, HarnessCursor, HarnessGemini} {
		t.Run(name, func(t *testing.T) {
			d, ok := DialectFor(name)
			if !ok {
				t.Fatalf("%s dialect not registered", name)
			}
			native, err := d.ToNative(cfg, existing)
			if err != nil {
				t.Fatal(err)
			}

			var doc map[string]any
			if err := json.Unmarshal(native, &doc); err != nil {
				t.Fatalf("result is not valid JSON: %v\n%s", err, native)
			}

			for _, key := range []string{"numStartups", "projects", "tipsHistory", "theme"} {
				if _, ok := doc[key]; !ok {
					t.Errorf("unrelated key %q was dropped:\n%s", key, native)
				}
			}
			if n, _ := doc["numStartups"].(float64); n != 42 {
				t.Errorf("numStartups = %v, want 42", doc["numStartups"])
			}
			if th, ok := doc["tipsHistory"].([]any); !ok || len(th) != 2 {
				t.Errorf("tipsHistory changed shape: %v", doc["tipsHistory"])
			}

			// The managed key itself must be replaced, not merged into.
			servers, _ := doc[jsonServersKey].(map[string]any)
			if _, stale := servers["stale"]; stale {
				t.Error("stale server survived — the managed key must be replaced wholesale")
			}
			if _, fresh := servers["fresh"]; !fresh {
				t.Error("new server missing from managed key")
			}
		})
	}
}

func TestTOMLDialect_PreservesUnrelatedKeys(t *testing.T) {
	existing := []byte(`model = "o3"
approval_policy = "on-request"

[sandbox]
mode = "workspace-write"

[mcp_servers.stale]
command = "old"
`)

	d, ok := DialectFor(HarnessCodex)
	if !ok {
		t.Fatal("codex dialect not registered")
	}
	cfg := Config{Servers: map[string]Server{
		"fresh": {Type: TypeStdio, Command: "new"},
	}}.Normalize()

	native, err := d.ToNative(cfg, existing)
	if err != nil {
		t.Fatal(err)
	}
	text := string(native)

	for _, want := range []string{`model = "o3"`, `approval_policy`, `[sandbox]`, `mode = "workspace-write"`} {
		if !strings.Contains(text, want) {
			t.Errorf("unrelated TOML content %q was dropped:\n%s", want, text)
		}
	}
	if strings.Contains(text, "stale") {
		t.Errorf("stale server survived the merge:\n%s", text)
	}
	if !strings.Contains(text, "fresh") {
		t.Errorf("new server missing:\n%s", text)
	}
}

// Non-deterministic output would produce spurious diffs on every apply and
// wake the watcher for no reason.
func TestDialects_OutputIsDeterministic(t *testing.T) {
	cfg := Config{Servers: map[string]Server{
		"zeta":  {Type: TypeStdio, Command: "z", Env: map[string]string{"B": "${B}", "A": "${A}", "C": "${C}"}},
		"alpha": {Type: TypeStdio, Command: "a", Args: []string{"one", "two"}},
		"mid":   {Type: TypeHTTP, URL: "https://m.example", Headers: map[string]string{"Z": "${Z}", "A": "${A}"}},
	}}.Normalize()

	for _, d := range Dialects() {
		t.Run(d.Name(), func(t *testing.T) {
			first, err := d.ToNative(cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				again, err := d.ToNative(cfg, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(first, again) {
					t.Fatalf("output differs between runs:\n%s\n---\n%s", first, again)
				}
			}

			// Server keys must be emitted in sorted order.
			text := string(first)
			ia, im, iz := strings.Index(text, "alpha"), strings.Index(text, "mid"), strings.Index(text, "zeta")
			if ia >= im || im >= iz {
				t.Errorf("server keys are not sorted (alpha=%d mid=%d zeta=%d):\n%s", ia, im, iz, text)
			}
		})
	}
}

// Re-applying an unchanged config over its own output must be a no-op, or the
// first apply after any write would report a spurious change.
func TestDialects_ReapplyOverOwnOutputIsStable(t *testing.T) {
	cfg := Config{Servers: map[string]Server{
		"github": {Type: TypeStdio, Command: "npx", Env: map[string]string{"T": "${env:T}"}},
	}}.Normalize()

	for _, d := range Dialects() {
		t.Run(d.Name(), func(t *testing.T) {
			first, err := d.ToNative(cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			second, err := d.ToNative(cfg, first)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, second) {
				t.Errorf("re-applying over own output changed it:\n%s\n---\n%s", first, second)
			}
		})
	}
}

// Adoption reads native files that weft did not write. A literal credential
// there must stop the import loudly rather than be copied into a source that
// may later be pushed to a git remote.
func TestDialects_ToCanonicalRejectsLiteralSecrets(t *testing.T) {
	jsonNative := []byte(`{"mcpServers":{"github":{"command":"npx","env":{"GITHUB_TOKEN":"ghp_S3cr3tV4lu3Th4tIsL0ngEnough"}}}}`)
	tomlNative := []byte("[mcp_servers.github]\ncommand = \"npx\"\n\n[mcp_servers.github.env]\nGITHUB_TOKEN = \"ghp_S3cr3tV4lu3Th4tIsL0ngEnough\"\n")

	for _, d := range Dialects() {
		t.Run(d.Name(), func(t *testing.T) {
			native := jsonNative
			if d.Name() == HarnessCodex {
				native = tomlNative
			}
			_, err := d.ToCanonical(native)
			if err == nil {
				t.Fatal("expected a literal credential to be rejected")
			}
			if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
				t.Errorf("error should name the offending variable, got: %v", err)
			}
		})
	}
}

// An indirection is the supported way to carry a secret and must pass cleanly.
func TestDialects_ToCanonicalAcceptsIndirections(t *testing.T) {
	native := []byte(`{"mcpServers":{"github":{"command":"npx","env":{"GITHUB_TOKEN":"${env:GITHUB_TOKEN}"}}}}`)

	d, ok := DialectFor(HarnessClaudeCode)
	if !ok {
		t.Fatal("claude-code dialect not registered")
	}
	cfg, err := d.ToCanonical(native)
	if err != nil {
		t.Fatalf("indirection should be accepted: %v", err)
	}
	if got := cfg.Servers["github"].Env["GITHUB_TOKEN"]; got != "${env:GITHUB_TOKEN}" {
		t.Errorf("env value = %q, want the indirection preserved", got)
	}
}

func TestDialectFor_UnknownHarness(t *testing.T) {
	if _, ok := DialectFor("windsurf"); ok {
		t.Error("windsurf has no MCP support and should not resolve a dialect")
	}
}
