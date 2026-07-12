package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTicketNew_scaffoldsFromTemplatesWithPlaceholder(t *testing.T) {
	base := withIsolatedConfig(t) // weft_home == base
	// Seed a custom ticket template to prove templates are used + placeholder filled.
	tmplDir := filepath.Join(base, "templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	writeFileT(t, filepath.Join(tmplDir, "ticket.md"), "# {{ticket}}\ncustom body")

	runCmd(t, ticketNewCmd, []string{"DIGI-100"})

	dir := filepath.Join(base, "work", "tickets", "DIGI-100")
	main := filepath.Join(dir, "DIGI-100.md")
	got, err := os.ReadFile(main)
	if err != nil {
		t.Fatalf("reading ticket main: %v", err)
	}
	if !strings.Contains(string(got), "# DIGI-100") || !strings.Contains(string(got), "custom body") {
		t.Errorf("ticket main not scaffolded from template with placeholder:\n%s", got)
	}
	for _, f := range []string{"estimate.md", "analysis.md", "plan.md"} {
		if !fileExists(filepath.Join(dir, f)) {
			t.Errorf("missing scaffolded artifact %s", f)
		}
	}
}

func TestTicketNew_usesFallbackWhenNoTemplate(t *testing.T) {
	base := withIsolatedConfig(t)
	runCmd(t, ticketNewCmd, []string{"ABC-1"})
	got, err := os.ReadFile(filepath.Join(base, "work", "tickets", "ABC-1", "ABC-1.md"))
	if err != nil {
		t.Fatalf("reading ticket: %v", err)
	}
	if !strings.Contains(string(got), "# ABC-1") {
		t.Errorf("built-in fallback not used / placeholder not filled:\n%s", got)
	}
}

func TestTicketNew_idempotentDoesNotOverwrite(t *testing.T) {
	base := withIsolatedConfig(t)
	runCmd(t, ticketNewCmd, []string{"X-9"})
	main := filepath.Join(base, "work", "tickets", "X-9", "X-9.md")
	if err := os.WriteFile(main, []byte("MY EDITS"), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}
	out := runCmd(t, ticketNewCmd, []string{"X-9"})
	if !strings.Contains(out, "already scaffolded") {
		t.Errorf("second run not idempotent:\n%s", out)
	}
	if got, _ := os.ReadFile(main); string(got) != "MY EDITS" {
		t.Errorf("ticket new overwrote edited file: %q", got)
	}
}

func TestTicketNew_rejectsInvalidID(t *testing.T) {
	withIsolatedConfig(t)
	holder := newHolderCmd()
	if err := ticketNewCmd.RunE(holder, []string{"bad/id"}); err == nil {
		t.Fatal("expected error for ticket id containing a path separator")
	}
}

func TestTicketList_showsScaffoldedTickets(t *testing.T) {
	withIsolatedConfig(t)
	runCmd(t, ticketNewCmd, []string{"T-2"})
	runCmd(t, ticketNewCmd, []string{"T-1"})
	out := runCmd(t, ticketListCmd, nil)
	if !strings.Contains(out, "T-1") || !strings.Contains(out, "T-2") {
		t.Errorf("ticket list missing entries:\n%s", out)
	}
	// Sorted output: T-1 before T-2.
	if strings.Index(out, "T-1") > strings.Index(out, "T-2") {
		t.Errorf("ticket list not sorted:\n%s", out)
	}
}

func TestInit_seedsTemplates(t *testing.T) {
	base := withIsolatedConfig(t)
	runCmd(t, initCmd, nil)
	for _, name := range []string{"ticket.md", "estimate.md", "analysis.md", "plan.md"} {
		if !fileExists(filepath.Join(base, "templates", name)) {
			t.Errorf("init did not seed template %s", name)
		}
	}
}
