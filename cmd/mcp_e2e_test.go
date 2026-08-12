package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

// canonicalMCPSource writes a source whose root carries an mcp.yaml alongside the
// instruction file, which is where a source declares its MCP servers.
func canonicalMCPSource(t *testing.T) []source.Source {
	t.Helper()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "# rules")
	writeFile(t, filepath.Join(root, "mcp.yaml"), `servers:
  github:
    type: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_TOKEN: ${env:GITHUB_TOKEN}
  remote:
    type: http
    url: https://example.com/mcp
`)

	return []source.Source{{Name: "testsrc", Root: root, Priority: 10, Structure: source.DefaultStructure()}}
}

// The full path from a source's mcp.yaml to a harness's native file, which is
// the path production uses and the one nothing covered (#233).
//
// Two separate defects made this silent. managedFilter dropped mcp.yaml before
// it reached staging, so stageMCPConfig always parsed nothing; and the Gemini
// dialect was registered as "gemini" while GeminiCLI.Name() returns
// "gemini-cli", so ProjectMCP took the no-MCP-support early return. Either one
// alone leaves settings.json without servers, so the assertion has to run
// end to end rather than on a hand-built Config.
func TestMergeAndApply_ProjectsMCPToGeminiSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	// Pre-existing settings Gemini owns. The projection is a keyed merge, so
	// these must survive.
	writeFile(t, filepath.Join(home, ".gemini", "settings.json"),
		`{"theme":"dark","selectedAuthType":"oauth-personal"}`)

	srcs := canonicalMCPSource(t)
	p := &profile.Profile{
		Name:    "mcptest",
		Sources: []string{"testsrc"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"gemini-cli"},
	}

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, true); err != nil {
		t.Fatalf("mergeAndApply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("gemini settings.json missing: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, data)
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if len(servers) != 2 {
		t.Fatalf("want 2 mcp servers, got %d:\n%s", len(servers), data)
	}
	if _, ok := servers["github"]; !ok {
		t.Errorf("github server missing:\n%s", data)
	}
	// Gemini encodes transport in the key name, so an http server must arrive as
	// httpUrl. Claude's shape here would be a file Gemini ignores.
	remote, _ := servers["remote"].(map[string]any)
	if remote["httpUrl"] != "https://example.com/mcp" {
		t.Errorf("http server must use the httpUrl key:\n%s", data)
	}

	if doc["theme"] != "dark" || doc["selectedAuthType"] != "oauth-personal" {
		t.Errorf("keyed merge dropped settings Gemini owns:\n%s", data)
	}
}

// mcp.yaml is projected through a dialect, never copied, so the staged copy must
// be consumed rather than landing in the target as a literal file.
func TestMergeAndApply_MCPDocumentIsNotCopiedToTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	srcs := canonicalMCPSource(t)
	p := &profile.Profile{
		Name:    "mcptest",
		Sources: []string{"testsrc"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"gemini-cli"},
	}

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, true); err != nil {
		t.Fatalf("mergeAndApply: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, ".gemini", "mcp.yaml")); err == nil {
		t.Error("mcp.yaml must not be copied verbatim into the target")
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "staged", "mcptest", "mcp.yaml")); err == nil {
		t.Error("staged mcp.yaml must be consumed by stageMCPConfig, not left behind")
	}
}
