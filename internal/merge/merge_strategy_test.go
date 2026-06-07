package merge_test

import (
	"bytes"
	"testing"

	"github.com/jophira/weft/internal/merge"
	"github.com/jophira/weft/internal/profile"
)

// ── CascadeStrategy ───────────────────────────────────────────────────────────

func TestCascadeStrategy_overlayWins(t *testing.T) {
	got, err := merge.CascadeStrategy([]byte("base"), []byte("overlay"))
	if err != nil {
		t.Fatalf("CascadeStrategy: %v", err)
	}
	if !bytes.Equal(got, []byte("overlay")) {
		t.Errorf("CascadeStrategy = %q, want %q", got, "overlay")
	}
}

func TestCascadeStrategy_emptyOverlayFallsBackToBase(t *testing.T) {
	got, err := merge.CascadeStrategy([]byte("base content"), nil)
	if err != nil {
		t.Fatalf("CascadeStrategy: %v", err)
	}
	if !bytes.Equal(got, []byte("base content")) {
		t.Errorf("CascadeStrategy(base, nil) = %q, want %q", got, "base content")
	}
}

func TestCascadeStrategy_bothEmpty(t *testing.T) {
	got, err := merge.CascadeStrategy(nil, nil)
	if err != nil {
		t.Fatalf("CascadeStrategy: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("CascadeStrategy(nil, nil) = %q, want nil/empty", got)
	}
}

// ── LastWinsStrategy ──────────────────────────────────────────────────────────

func TestLastWinsStrategy_alwaysReturnsOverlay(t *testing.T) {
	got, err := merge.LastWinsStrategy([]byte("base"), []byte("overlay"))
	if err != nil {
		t.Fatalf("LastWinsStrategy: %v", err)
	}
	if !bytes.Equal(got, []byte("overlay")) {
		t.Errorf("LastWinsStrategy = %q, want %q", got, "overlay")
	}
}

func TestLastWinsStrategy_emptyOverlay(t *testing.T) {
	got, err := merge.LastWinsStrategy([]byte("base"), nil)
	if err != nil {
		t.Fatalf("LastWinsStrategy: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("LastWinsStrategy(base, nil) = %q, want empty", got)
	}
}

// ── AppendStrategy ────────────────────────────────────────────────────────────

func TestAppendStrategy_emptyBase(t *testing.T) {
	got, err := merge.AppendStrategy(nil, []byte("overlay"))
	if err != nil {
		t.Fatalf("AppendStrategy: %v", err)
	}
	if !bytes.Equal(got, []byte("overlay")) {
		t.Errorf("AppendStrategy(nil, overlay) = %q, want %q", got, "overlay")
	}
}

func TestAppendStrategy_emptyOverlay(t *testing.T) {
	got, err := merge.AppendStrategy([]byte("base"), nil)
	if err != nil {
		t.Fatalf("AppendStrategy: %v", err)
	}
	if !bytes.Equal(got, []byte("base")) {
		t.Errorf("AppendStrategy(base, nil) = %q, want %q", got, "base")
	}
}

func TestAppendStrategy_addsNewlineWhenMissing(t *testing.T) {
	got, err := merge.AppendStrategy([]byte("base"), []byte("overlay"))
	if err != nil {
		t.Fatalf("AppendStrategy: %v", err)
	}
	want := "base\noverlay"
	if string(got) != want {
		t.Errorf("AppendStrategy = %q, want %q", got, want)
	}
}

func TestAppendStrategy_noDoubleNewline(t *testing.T) {
	got, err := merge.AppendStrategy([]byte("base\n"), []byte("overlay"))
	if err != nil {
		t.Fatalf("AppendStrategy: %v", err)
	}
	want := "base\noverlay"
	if string(got) != want {
		t.Errorf("AppendStrategy = %q, want %q (no double newline)", got, want)
	}
}

// ── ForOverlay ────────────────────────────────────────────────────────────────

func TestForOverlay_cascade(t *testing.T) {
	s := merge.ForOverlay(profile.OverlayCascade)
	if s == nil {
		t.Fatal("ForOverlay(cascade) returned nil")
	}
}

func TestForOverlay_merge(t *testing.T) {
	s := merge.ForOverlay(profile.OverlayMerge)
	if s == nil {
		t.Fatal("ForOverlay(merge) returned nil")
	}
}

func TestForOverlay_lastWins(t *testing.T) {
	s := merge.ForOverlay(profile.OverlayLastWins)
	if s == nil {
		t.Fatal("ForOverlay(lastWins) returned nil")
	}
}

func TestForOverlay_unknown_defaultsCascade(t *testing.T) {
	s := merge.ForOverlay("unknown-overlay")
	// Unknown overlay should default to CascadeStrategy behaviour.
	got, _ := s([]byte("base"), []byte("overlay"))
	if !bytes.Equal(got, []byte("overlay")) {
		t.Errorf("default overlay strategy = %q, want overlay to win (cascade behaviour)", got)
	}
}
