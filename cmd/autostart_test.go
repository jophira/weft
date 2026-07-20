package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/autostart"
)

func TestAutostartProfileLabel_should_name_the_selection_mode(t *testing.T) {
	if got := autostartProfileLabel(""); !strings.Contains(got, "follow-active") {
		t.Errorf("unpinned label = %q, want it to mention follow-active", got)
	}
	if got := autostartProfileLabel("work"); !strings.Contains(got, "pinned") || !strings.Contains(got, "work") {
		t.Errorf("pinned label = %q, want the name and 'pinned'", got)
	}
}

func TestRenderAutostartStatus_should_point_at_enable_when_not_installed(t *testing.T) {
	var buf bytes.Buffer
	renderAutostartStatus(&buf, autostart.Status{Mechanism: "systemd user unit"})

	out := buf.String()
	if !strings.Contains(out, "not installed") {
		t.Errorf("output should say not installed:\n%s", out)
	}
	if !strings.Contains(out, "weft autostart enable") {
		t.Errorf("output should suggest the next step:\n%s", out)
	}
}

func TestRenderAutostartStatus_should_mark_a_stale_binary_and_show_notes(t *testing.T) {
	var buf bytes.Buffer
	renderAutostartStatus(&buf, autostart.Status{
		Mechanism: "systemd user unit",
		UnitPath:  "/home/u/.config/systemd/user/weft.service",
		Installed: true,
		BinPath:   "/gone/weft",
		Stale:     true,
		Notes:     []string{"recorded binary /gone/weft no longer exists"},
	})

	out := buf.String()
	for _, want := range []string{"installed, not running", "(stale)", "no longer exists"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderAutostartStatus_should_report_a_healthy_running_service(t *testing.T) {
	var buf bytes.Buffer
	renderAutostartStatus(&buf, autostart.Status{
		Mechanism: "systemd user unit",
		UnitPath:  "/u/weft.service",
		Installed: true,
		Running:   true,
		BinPath:   "/usr/local/bin/weft",
		Profile:   "work",
	})

	out := buf.String()
	if !strings.Contains(out, "installed and running") {
		t.Errorf("output should report running:\n%s", out)
	}
	if strings.Contains(out, "(stale)") {
		t.Errorf("healthy status must not be marked stale:\n%s", out)
	}
	if !strings.Contains(out, "work (pinned)") {
		t.Errorf("output should name the pinned profile:\n%s", out)
	}
}
