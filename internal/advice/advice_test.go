package advice

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClock returns a Now function pinned to t, so throttle windows can be
// crossed without sleeping.
func fakeClock(at *time.Time) func() time.Time {
	return func() time.Time { return *at }
}

func TestEmit_printsMessageAndFix(t *testing.T) {
	var buf bytes.Buffer
	b := New(Options{})
	b.Add(Advice{Code: "W001", Severity: Warn, Message: "something to know", Fix: "run 'weft doctor'"})
	b.Emit(&buf)

	out := buf.String()
	if !strings.Contains(out, "something to know") {
		t.Errorf("output %q missing the message", out)
	}
	if !strings.Contains(out, "[W001]") {
		t.Errorf("output %q missing the code, which is what the user mutes", out)
	}
	if !strings.Contains(out, "run 'weft doctor'") {
		t.Errorf("output %q missing the fix", out)
	}
}

func TestEmit_nothingCollectedPrintsNothing(t *testing.T) {
	var buf bytes.Buffer
	New(Options{}).Emit(&buf)
	if buf.Len() != 0 {
		t.Errorf("Emit with no advice wrote %q, want nothing", buf.String())
	}
}

func TestAdd_ignoresIncompleteEntries(t *testing.T) {
	b := New(Options{})
	b.Add(Advice{Code: "", Message: "no code"})
	b.Add(Advice{Code: "W001", Message: ""})
	if b.Len() != 0 {
		t.Errorf("Len() = %d, want 0 — entries without a code or message are unusable", b.Len())
	}
}

func TestAdd_dedupesByCodeWithinARun(t *testing.T) {
	b := New(Options{})
	// A loop over several harnesses tripping the same condition must not turn
	// into several identical lines.
	b.Add(Advice{Code: "W001", Message: "first"})
	b.Add(Advice{Code: "W001", Message: "second"})
	if b.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", b.Len())
	}

	var buf bytes.Buffer
	b.Emit(&buf)
	if !strings.Contains(buf.String(), "first") {
		t.Errorf("output %q should keep the first occurrence", buf.String())
	}
}

func TestEmit_capsPrintedHintsButKeepsHighestSeverity(t *testing.T) {
	var buf bytes.Buffer
	b := New(Options{Max: 2})
	b.Add(Advice{Code: "I1", Severity: Info, Message: "info one"})
	b.Add(Advice{Code: "I2", Severity: Info, Message: "info two"})
	b.Add(Advice{Code: "E1", Severity: Error, Message: "the important one"})
	b.Emit(&buf)

	out := buf.String()
	if !strings.Contains(out, "the important one") {
		t.Errorf("output %q dropped the highest-severity hint, which the cap must never do", out)
	}
	if strings.Count(out, "[") != 2 {
		t.Errorf("output %q printed a number of hints other than the cap of 2", out)
	}
}

func TestEmit_mutedCodeNeverPrints(t *testing.T) {
	var buf bytes.Buffer
	b := New(Options{Muted: []string{"W001"}})
	b.Add(Advice{Code: "W001", Message: "muted hint"})
	b.Add(Advice{Code: "W002", Message: "visible hint"})
	b.Emit(&buf)

	out := buf.String()
	if strings.Contains(out, "muted hint") {
		t.Errorf("output %q printed a muted code", out)
	}
	if !strings.Contains(out, "visible hint") {
		t.Errorf("output %q suppressed an unmuted code", out)
	}
}

func TestEmit_throttlesWithinWindowThenSpeaksAgain(t *testing.T) {
	now := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	store := NewFileStore(t.TempDir())
	b := New(Options{Throttle: time.Hour, Store: store, Now: fakeClock(&now)})

	var first bytes.Buffer
	b.Add(Advice{Code: "W001", Message: "recurring hint"})
	b.Emit(&first)
	if !strings.Contains(first.String(), "recurring hint") {
		t.Fatalf("first emit = %q, want the hint on its first occurrence", first.String())
	}

	// Inside the window: the watcher re-applying every few seconds must not
	// repeat itself.
	now = now.Add(30 * time.Minute)
	var second bytes.Buffer
	b.Add(Advice{Code: "W001", Message: "recurring hint"})
	b.Emit(&second)
	if second.Len() != 0 {
		t.Errorf("second emit = %q, want silence inside the throttle window", second.String())
	}

	// Past the window: it is worth saying again.
	now = now.Add(90 * time.Minute)
	var third bytes.Buffer
	b.Add(Advice{Code: "W001", Message: "recurring hint"})
	b.Emit(&third)
	if !strings.Contains(third.String(), "recurring hint") {
		t.Errorf("third emit = %q, want the hint again once the window passed", third.String())
	}
}

func TestEmit_withoutStoreNeverThrottles(t *testing.T) {
	// A missing config dir yields a nil store. Repeating a hint is the right
	// failure mode there; losing it is not.
	b := New(Options{})
	for i := 0; i < 3; i++ {
		var buf bytes.Buffer
		b.Add(Advice{Code: "W001", Message: "always shown"})
		b.Emit(&buf)
		if !strings.Contains(buf.String(), "always shown") {
			t.Fatalf("emit %d = %q, want the hint every time without a store", i, buf.String())
		}
	}
}

// ── FileStore ─────────────────────────────────────────────────────────────────

func TestFileStore_persistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)

	if err := NewFileStore(dir).MarkShown("W001", at); err != nil {
		t.Fatalf("MarkShown: %v", err)
	}

	// A CLI run lasts milliseconds, so throttling only works if it survives the
	// process. This is the assertion that matters.
	got, ok := NewFileStore(dir).LastShown("W001")
	if !ok {
		t.Fatal("LastShown reported nothing after MarkShown in a previous instance")
	}
	if !got.Equal(at) {
		t.Errorf("LastShown = %v, want %v", got, at)
	}
}

func TestFileStore_unknownCodeReportsNotShown(t *testing.T) {
	if _, ok := NewFileStore(t.TempDir()).LastShown("W999"); ok {
		t.Error("LastShown reported a code that was never shown")
	}
}

func TestFileStore_corruptFileIsDroppedNotFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	s := NewFileStore(dir)
	if _, ok := s.LastShown("W001"); ok {
		t.Error("corrupt state produced a timestamp, want an empty store")
	}
	// And the store still works afterwards rather than failing the same way forever.
	if err := s.MarkShown("W001", time.Now()); err != nil {
		t.Errorf("MarkShown after corrupt load: %v", err)
	}
}

func TestFileStore_resetForgetsCode(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)
	if err := s.MarkShown("W001", time.Now()); err != nil {
		t.Fatalf("MarkShown: %v", err)
	}
	if err := s.Reset("W001"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, ok := s.LastShown("W001"); ok {
		t.Error("code still recorded after Reset")
	}
}
