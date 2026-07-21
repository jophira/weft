package harness

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/mcpconfig"
	"github.com/jophira/weft/internal/testenv"
)

func sampleMCP() mcpconfig.Config {
	return mcpconfig.Config{Servers: map[string]mcpconfig.Server{
		"github": {Type: mcpconfig.TypeStdio, Command: "npx"},
	}}.Normalize()
}

func TestProjectMCP_WritesNativeFile(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	ctx := ApplyCtx{ProfileName: "p", CfgDir: t.TempDir()}

	if err := ProjectMCP(&ClaudeCode{}, sampleMCP(), ctx); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("native mcp file not written: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Errorf("github server missing from %s", data)
	}
}

// The destination is a document the tool owns; weft merges into its key and must
// leave everything else alone.
func TestProjectMCP_PreservesUnrelatedState(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	path := filepath.Join(home, ".claude.json")
	write(t, path, `{"numStartups":7,"projects":{"/x":{"a":1}}}`)

	ctx := ApplyCtx{ProfileName: "p", CfgDir: t.TempDir()}
	if err := ProjectMCP(&ClaudeCode{}, sampleMCP(), ctx); err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if n, _ := doc["numStartups"].(float64); n != 7 {
		t.Errorf("numStartups lost or changed: %v", doc["numStartups"])
	}
	if _, ok := doc["projects"]; !ok {
		t.Errorf("projects key was dropped:\n%s", data)
	}
}

// A harness with no MCP dialect must be left untouched rather than guessed at.
func TestProjectMCP_NoDialectIsNoOp(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	ctx := ApplyCtx{ProfileName: "p", CfgDir: t.TempDir()}

	if err := ProjectMCP(&Windsurf{}, sampleMCP(), ctx); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Errorf("nothing should have been written for a harness with no dialect, got %v", entries)
	}
}

func TestProjectMCP_RespectsHarnessSyncExclusion(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	ctx := ApplyCtx{
		ProfileName:    "p",
		CfgDir:         t.TempDir(),
		AllowedClasses: map[Class]bool{ClassInstructions: true}, // mcp withheld
	}

	if err := ProjectMCP(&ClaudeCode{}, sampleMCP(), ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err == nil {
		t.Error("mcp is withheld by config and must not be written")
	}
}

// Re-projecting identical config must not rewrite the file, or every apply would
// look like a change and wake the watcher.
func TestProjectMCP_UnchangedOnSecondApply(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	cfgDir := t.TempDir()
	path := filepath.Join(home, ".claude.json")

	buf := &bytes.Buffer{}
	ctx := ApplyCtx{ProfileName: "p", CfgDir: cfgDir, Out: buf}
	if err := ProjectMCP(&ClaudeCode{}, sampleMCP(), ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), statusWrote) {
		t.Fatalf("first apply should write, got %q", buf.String())
	}

	buf.Reset()
	if err := ProjectMCP(&ClaudeCode{}, sampleMCP(), ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), statusUnchanged) {
		t.Errorf("identical config should report unchanged, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "backed up") {
		t.Errorf("weft's own output must not be mistaken for an external edit: %q", buf.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// The sidecar key must not join the staged set: pruneDropped would then delete
// the native file on the next apply.
func TestProjectMCP_DoesNotJoinStagedSet(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	cfgDir := t.TempDir()

	ctx := ApplyCtx{ProfileName: "p", CfgDir: cfgDir}
	if err := ProjectMCP(&ClaudeCode{}, sampleMCP(), ctx); err != nil {
		t.Fatal(err)
	}

	m, err := manifest.Load(cfgDir, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range m.Staged {
		if s == mcpManifestKey(filepath.Join(home, ".claude.json")) {
			t.Error("mcp sidecar must not be in the staged set")
		}
	}
	if len(m.Files) == 0 {
		t.Error("mcp sidecar should still be recorded in Files for ownership")
	}
}

// mcp.yaml is projected through a dialect, so it must never be copied verbatim
// into a target the way an unclassified root file would be.
func TestStagedClass_MCPDocument(t *testing.T) {
	if got := stagedClass("mcp.yaml"); got != ClassMCP {
		t.Errorf("stagedClass(mcp.yaml) = %q, want %q", got, ClassMCP)
	}
	if _, ok := routeStaged("mcp.yaml", nil, &ClaudeCode{}); ok {
		t.Error("mcp.yaml must not be routed to a literal target path")
	}
}
