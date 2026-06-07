package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// ── runDoctor ─────────────────────────────────────────────────────────────────

func TestRunDoctor_containsHealthCheckHeader(t *testing.T) {
	var buf bytes.Buffer
	runDoctor(&buf)
	if !strings.Contains(buf.String(), "Jophira Health Check") {
		t.Errorf("runDoctor: missing health check header\ngot:\n%s", buf.String())
	}
}

func TestRunDoctor_containsConfigDirSection(t *testing.T) {
	var buf bytes.Buffer
	runDoctor(&buf)
	if !strings.Contains(buf.String(), "Config dir") {
		t.Errorf("runDoctor: missing 'Config dir' line\ngot:\n%s", buf.String())
	}
}

func TestRunDoctor_containsHarnessScan(t *testing.T) {
	var buf bytes.Buffer
	runDoctor(&buf)
	if !strings.Contains(buf.String(), "Scanning for AI rule folders") {
		t.Errorf("runDoctor: missing harness scan section\ngot:\n%s", buf.String())
	}
}

func TestRunDoctor_writesToSuppliedWriter(t *testing.T) {
	var buf bytes.Buffer
	runDoctor(&buf)
	if buf.Len() == 0 {
		t.Error("runDoctor: wrote nothing to the supplied writer")
	}
}
