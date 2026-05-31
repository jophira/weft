package hook_test

import (
	"os"
	"testing"

	"github.com/jophira/weft/internal/hook"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newManager(t *testing.T) *hook.FileManager {
	t.Helper()
	return hook.NewFileManager(t.TempDir())
}

func shellHook(name string) hook.Hook {
	return hook.Hook{
		Name:    name,
		Trigger: hook.TriggerManual,
		Action:  hook.Action{Type: hook.ActionShell, Command: "echo hi"},
	}
}

// ── Add ───────────────────────────────────────────────────────────────────────

func TestAdd_persists(t *testing.T) {
	m := newManager(t)
	h := shellHook("sync")
	if err := m.Add(h); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := m.Get("sync")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "sync" || got.Action.Command != "echo hi" {
		t.Fatalf("unexpected hook: %+v", got)
	}
}

func TestAdd_duplicateReturnsError(t *testing.T) {
	m := newManager(t)
	h := shellHook("dup")
	if err := m.Add(h); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(h); err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
}

func TestAdd_invalidNames(t *testing.T) {
	m := newManager(t)
	bad := []string{"", "Has-Upper", "1starts-digit", "has space", "has/slash"}
	for _, name := range bad {
		h := shellHook(name)
		if err := m.Add(h); err == nil {
			t.Errorf("expected error for name %q, got nil", name)
		}
	}
}

func TestAdd_invalidTrigger(t *testing.T) {
	m := newManager(t)
	h := hook.Hook{
		Name:    "bad",
		Trigger: "not-a-trigger",
		Action:  hook.Action{Type: hook.ActionShell, Command: "x"},
	}
	if err := m.Add(h); err == nil {
		t.Fatal("expected error for invalid trigger, got nil")
	}
}

func TestAdd_invalidAction(t *testing.T) {
	m := newManager(t)
	h := hook.Hook{
		Name:    "bad",
		Trigger: hook.TriggerManual,
		Action:  hook.Action{Type: "not-an-action"},
	}
	if err := m.Add(h); err == nil {
		t.Fatal("expected error for invalid action, got nil")
	}
}

func TestAdd_shellRequiresCommand(t *testing.T) {
	m := newManager(t)
	h := hook.Hook{
		Name:    "no-cmd",
		Trigger: hook.TriggerManual,
		Action:  hook.Action{Type: hook.ActionShell},
	}
	if err := m.Add(h); err == nil {
		t.Fatal("expected error for missing command, got nil")
	}
}

func TestAdd_appendMemoryRequiresSourceAndSummaryTo(t *testing.T) {
	m := newManager(t)
	base := hook.Hook{
		Name:    "mem",
		Trigger: hook.TriggerManual,
		Prompt:  "some content",
		Action: hook.Action{
			Type:         hook.ActionAppendMemory,
			TargetSource: "personal",
			SummaryTo:    "memory/LOG.md",
		},
	}

	// Missing source.
	h := base
	h.Action.TargetSource = ""
	if err := m.Add(h); err == nil {
		t.Error("expected error for missing target_source, got nil")
	}

	// Missing summary_to.
	h = base
	h.Action.SummaryTo = ""
	if err := m.Add(h); err == nil {
		t.Error("expected error for missing summary_to, got nil")
	}
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestGet_notFound(t *testing.T) {
	m := newManager(t)
	if _, err := m.Get("missing"); err == nil {
		t.Fatal("expected error for missing hook, got nil")
	}
}

// ── Remove ────────────────────────────────────────────────────────────────────

func TestRemove_removes(t *testing.T) {
	m := newManager(t)
	if err := m.Add(shellHook("gone")); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("gone"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := m.Get("gone"); err == nil {
		t.Fatal("expected hook to be gone after Remove")
	}
}

func TestRemove_notFound(t *testing.T) {
	m := newManager(t)
	if err := m.Remove("nope"); err == nil {
		t.Fatal("expected error removing non-existent hook, got nil")
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestList_empty(t *testing.T) {
	m := newManager(t)
	hooks, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 0 {
		t.Fatalf("expected 0 hooks, got %d", len(hooks))
	}
}

func TestList_returnsAll(t *testing.T) {
	m := newManager(t)
	names := []string{"alpha", "beta", "gamma"}
	for _, n := range names {
		if err := m.Add(shellHook(n)); err != nil {
			t.Fatal(err)
		}
	}
	hooks, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != len(names) {
		t.Fatalf("expected %d hooks, got %d", len(names), len(hooks))
	}
}

func TestList_missingDirIsEmpty(t *testing.T) {
	m := hook.NewFileManager("/tmp/weft-test-nonexistent-" + t.Name())
	hooks, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 0 {
		t.Fatalf("expected 0, got %d", len(hooks))
	}
}

// ── AllTriggers / actions round-trip ─────────────────────────────────────────

func TestAdd_allTriggersAccepted(t *testing.T) {
	triggers := []hook.Trigger{
		hook.TriggerManual,
		hook.TriggerSessionEnd,
		hook.TriggerFileChange,
		hook.TriggerPostCommit,
	}
	for _, tr := range triggers {
		m := newManager(t)
		h := hook.Hook{
			Name:    "h",
			Trigger: tr,
			Action:  hook.Action{Type: hook.ActionShell, Command: "x"},
		}
		if err := m.Add(h); err != nil {
			t.Errorf("trigger %q unexpectedly rejected: %v", tr, err)
		}
	}
}

func TestAdd_requireConfirmRoundtrip(t *testing.T) {
	m := newManager(t)
	h := hook.Hook{
		Name:    "confirm-me",
		Trigger: hook.TriggerManual,
		Action:  hook.Action{Type: hook.ActionShell, Command: "true", RequireConfirm: true},
	}
	if err := m.Add(h); err != nil {
		t.Fatal(err)
	}
	got, err := m.Get("confirm-me")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Action.RequireConfirm {
		t.Fatal("RequireConfirm not persisted")
	}
}

// ── executor tests ────────────────────────────────────────────────────────────

func newExecutor(t *testing.T) (*hook.Executor, string) {
	t.Helper()
	sourcesDir := t.TempDir()
	return hook.NewExecutor(sourcesDir), sourcesDir
}

func TestExecutor_shell_success(t *testing.T) {
	exec, _ := newExecutor(t)
	h := hook.Hook{
		Name:    "ok",
		Trigger: hook.TriggerManual,
		Action:  hook.Action{Type: hook.ActionShell, Command: "true"},
	}
	if err := exec.Run(h); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestExecutor_shell_nonZeroExit(t *testing.T) {
	exec, _ := newExecutor(t)
	h := hook.Hook{
		Name:    "fail",
		Trigger: hook.TriggerManual,
		Action:  hook.Action{Type: hook.ActionShell, Command: "exit 1"},
	}
	if err := exec.Run(h); err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
}

func TestExecutor_aiImprove_notImplemented(t *testing.T) {
	exec, _ := newExecutor(t)
	h := hook.Hook{
		Name:    "ai",
		Trigger: hook.TriggerManual,
		Action:  hook.Action{Type: hook.ActionAIImprove},
	}
	if err := exec.Run(h); err == nil {
		t.Fatal("expected not-implemented error, got nil")
	}
}

func TestExecutor_unknownAction(t *testing.T) {
	exec, _ := newExecutor(t)
	h := hook.Hook{
		Name:    "mystery",
		Trigger: hook.TriggerManual,
		Action:  hook.Action{Type: "unknown"},
	}
	if err := exec.Run(h); err == nil {
		t.Fatal("expected error for unknown action, got nil")
	}
}

func TestExecutor_appendMemory_createsFile(t *testing.T) {
	exec, sourcesDir := newExecutor(t)

	// Write a minimal source YAML so the executor can resolve the root.
	sourceRoot := t.TempDir()
	sourceYAML := "name: personal\nroot: " + sourceRoot + "\n"
	if err := os.WriteFile(sourcesDir+"/personal.yaml", []byte(sourceYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	h := hook.Hook{
		Name:    "log",
		Trigger: hook.TriggerManual,
		Prompt:  "Today I learned something useful.",
		Action: hook.Action{
			Type:         hook.ActionAppendMemory,
			TargetSource: "personal",
			SummaryTo:    "memory/LOG.md",
		},
	}
	if err := exec.Run(h); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(sourceRoot + "/memory/LOG.md")
	if err != nil {
		t.Fatalf("expected LOG.md to exist: %v", err)
	}
	if !contains(string(data), "Today I learned something useful.") {
		t.Fatalf("expected prompt content in LOG.md, got:\n%s", data)
	}
}

func TestExecutor_appendMemory_appendsToExisting(t *testing.T) {
	exec, sourcesDir := newExecutor(t)
	sourceRoot := t.TempDir()
	os.WriteFile(sourcesDir+"/personal.yaml", []byte("name: personal\nroot: "+sourceRoot+"\n"), 0o644)
	os.MkdirAll(sourceRoot+"/memory", 0o755)
	os.WriteFile(sourceRoot+"/memory/LOG.md", []byte("existing content\n"), 0o644)

	h := hook.Hook{
		Name:    "log",
		Trigger: hook.TriggerManual,
		Prompt:  "new entry",
		Action: hook.Action{
			Type:         hook.ActionAppendMemory,
			TargetSource: "personal",
			SummaryTo:    "memory/LOG.md",
		},
	}
	if err := exec.Run(h); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(sourceRoot + "/memory/LOG.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !contains(s, "existing content") {
		t.Error("original content was overwritten")
	}
	if !contains(s, "new entry") {
		t.Error("new entry not found")
	}
}

func TestExecutor_appendMemory_missingSource(t *testing.T) {
	exec, _ := newExecutor(t)
	h := hook.Hook{
		Name:    "log",
		Trigger: hook.TriggerManual,
		Prompt:  "content",
		Action: hook.Action{
			Type:         hook.ActionAppendMemory,
			TargetSource: "ghost",
			SummaryTo:    "memory/LOG.md",
		},
	}
	if err := exec.Run(h); err == nil {
		t.Fatal("expected error for missing source, got nil")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
