package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

// firstText extracts the text body from a successful tool result.
func firstText(t *testing.T, r *mcplib.CallToolResult) string {
	t.Helper()
	if len(r.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	tc, ok := mcplib.AsTextContent(r.Content[0])
	if !ok {
		t.Fatalf("content[0] is not TextContent: %T", r.Content[0])
	}
	return tc.Text
}

// callReq builds a CallToolRequest with the given key-value arguments.
func callReq(args map[string]any) mcplib.CallToolRequest {
	return mcplib.CallToolRequest{
		Params: mcplib.CallToolParams{Arguments: args},
	}
}

func newTestPM(t *testing.T) *profile.FileManager {
	t.Helper()
	return profile.NewFileManager(filepath.Join(t.TempDir(), "profiles"))
}

func newTestReg(t *testing.T) *source.FileRegistry {
	t.Helper()
	return source.NewFileRegistry(filepath.Join(t.TempDir(), "sources"))
}

// ── profileListHandler ────────────────────────────────────────────────────────

func TestProfileListHandler_empty(t *testing.T) {
	h := profileListHandler(newTestPM(t), func() string { return "" })
	result, err := h(context.Background(), callReq(nil))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := firstText(t, result)
	var out []any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result is not JSON array: %v — got: %q", err, text)
	}
	if len(out) != 0 {
		t.Errorf("expected empty array, got %d items", len(out))
	}
}

func TestProfileListHandler_withProfiles(t *testing.T) {
	pm := newTestPM(t)
	if err := pm.Create(profile.Profile{Name: "work", Sources: []string{"src"}, Overlay: profile.OverlayCascade}); err != nil {
		t.Fatal(err)
	}
	if err := pm.Create(profile.Profile{Name: "home", Sources: []string{"src"}, Overlay: profile.OverlayCascade}); err != nil {
		t.Fatal(err)
	}
	h := profileListHandler(pm, func() string { return "work" })
	result, err := h(context.Background(), callReq(nil))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := firstText(t, result)
	if !strings.Contains(text, `"work"`) {
		t.Errorf("result missing 'work': %q", text)
	}
	if !strings.Contains(text, `"is_active": true`) {
		t.Errorf("result missing is_active true: %q", text)
	}
	if !strings.Contains(text, `"home"`) {
		t.Errorf("result missing 'home': %q", text)
	}
}

// ── profileInspectHandler ─────────────────────────────────────────────────────

func TestProfileInspectHandler_emptyName(t *testing.T) {
	h := profileInspectHandler(newTestPM(t), func() string { return "" })
	result, err := h(context.Background(), callReq(map[string]any{"name": ""}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for empty name")
	}
}

func TestProfileInspectHandler_notFound(t *testing.T) {
	h := profileInspectHandler(newTestPM(t), func() string { return "" })
	result, err := h(context.Background(), callReq(map[string]any{"name": "nosuchprofile"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing profile")
	}
}

func TestProfileInspectHandler_found(t *testing.T) {
	pm := newTestPM(t)
	if err := pm.Create(profile.Profile{Name: "solo", Sources: []string{"a"}, Overlay: profile.OverlayCascade}); err != nil {
		t.Fatal(err)
	}
	h := profileInspectHandler(pm, func() string { return "solo" })
	result, err := h(context.Background(), callReq(map[string]any{"name": "solo"}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected IsError: %s", firstText(t, result))
	}
	if !strings.Contains(firstText(t, result), `"solo"`) {
		t.Errorf("result missing profile name: %q", firstText(t, result))
	}
}

// ── sourceListHandler ─────────────────────────────────────────────────────────

func TestSourceListHandler_empty(t *testing.T) {
	h := sourceListHandler(newTestReg(t))
	result, err := h(context.Background(), callReq(nil))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := firstText(t, result)
	var out []any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result is not JSON array: %v — got: %q", err, text)
	}
	if len(out) != 0 {
		t.Errorf("expected empty array, got %d items", len(out))
	}
}

func TestSourceListHandler_withSources(t *testing.T) {
	reg := newTestReg(t)
	root := t.TempDir()
	if err := reg.Add(source.Source{Name: "personal", Root: root, Branch: "main", Structure: source.DefaultStructure()}); err != nil {
		t.Fatal(err)
	}
	h := sourceListHandler(reg)
	result, err := h(context.Background(), callReq(nil))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !strings.Contains(firstText(t, result), `"personal"`) {
		t.Errorf("result missing source name: %q", firstText(t, result))
	}
}

// ── sourceStatusHandler ───────────────────────────────────────────────────────

func TestSourceStatusHandler_emptyName(t *testing.T) {
	h := sourceStatusHandler(newTestReg(t))
	result, err := h(context.Background(), callReq(map[string]any{"name": ""}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for empty name")
	}
}

func TestSourceStatusHandler_notFound(t *testing.T) {
	h := sourceStatusHandler(newTestReg(t))
	result, err := h(context.Background(), callReq(map[string]any{"name": "ghost"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing source")
	}
}

func TestSourceStatusHandler_nonGitDir(t *testing.T) {
	reg := newTestReg(t)
	root := t.TempDir() // plain directory, not a git repo
	if err := reg.Add(source.Source{Name: "local", Root: root, Branch: "main", Structure: source.DefaultStructure()}); err != nil {
		t.Fatal(err)
	}
	h := sourceStatusHandler(reg)
	result, err := h(context.Background(), callReq(map[string]any{"name": "local"}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected IsError for non-git dir: %s", firstText(t, result))
	}
	// Non-git dir → status field is empty string, dirty=false
	text := firstText(t, result)
	if !strings.Contains(text, `"local"`) {
		t.Errorf("result missing source name: %q", text)
	}
}

// ── sourceSyncHandler ─────────────────────────────────────────────────────────

func TestSourceSyncHandler_emptyRegistryAllSources(t *testing.T) {
	// name="" → sync all; empty registry → empty results, no error
	h := sourceSyncHandler(newTestReg(t))
	result, err := h(context.Background(), callReq(map[string]any{"name": ""}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error result: %s", firstText(t, result))
	}
	var out []any
	if err := json.Unmarshal([]byte(firstText(t, result)), &out); err != nil {
		t.Fatalf("result not JSON array: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty results, got %d", len(out))
	}
}

func TestSourceSyncHandler_notFound(t *testing.T) {
	h := sourceSyncHandler(newTestReg(t))
	result, err := h(context.Background(), callReq(map[string]any{"name": "ghost"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing source")
	}
}

// ── sourcePushHandler ─────────────────────────────────────────────────────────

func TestSourcePushHandler_emptyName(t *testing.T) {
	h := sourcePushHandler(newTestReg(t))
	result, err := h(context.Background(), callReq(map[string]any{"name": "", "message": "msg"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for empty name")
	}
}

func TestSourcePushHandler_emptyMessage(t *testing.T) {
	reg := newTestReg(t)
	if err := reg.Add(source.Source{Name: "src", Root: t.TempDir(), Branch: "main", Structure: source.DefaultStructure()}); err != nil {
		t.Fatal(err)
	}
	h := sourcePushHandler(reg)
	result, err := h(context.Background(), callReq(map[string]any{"name": "src", "message": ""}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for empty message")
	}
}

func TestSourcePushHandler_notFound(t *testing.T) {
	h := sourcePushHandler(newTestReg(t))
	result, err := h(context.Background(), callReq(map[string]any{"name": "ghost", "message": "update rules"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing source")
	}
}

// ── doctorHandler ─────────────────────────────────────────────────────────────

func TestDoctorHandler_runsWithoutPanic(t *testing.T) {
	pm := newTestPM(t)
	h := doctorHandler(func() string { return "" }, pm)
	result, err := h(context.Background(), callReq(nil))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected IsError: %s", firstText(t, result))
	}
	text := firstText(t, result)
	if !strings.Contains(text, `"config_ok"`) {
		t.Errorf("result missing config_ok: %q", text)
	}
}

func TestDoctorHandler_withActiveProfile(t *testing.T) {
	pm := newTestPM(t)
	if err := pm.Create(profile.Profile{Name: "p", Sources: []string{"src"}, Overlay: profile.OverlayCascade}); err != nil {
		t.Fatal(err)
	}
	h := doctorHandler(func() string { return "p" }, pm)
	result, err := h(context.Background(), callReq(nil))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := firstText(t, result)
	if !strings.Contains(text, `"active_profile"`) {
		t.Errorf("result missing active_profile: %q", text)
	}
}
