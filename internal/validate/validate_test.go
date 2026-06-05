package validate_test

import (
	"strings"
	"testing"

	"github.com/jophira/weft/internal/validate"
)

func TestInstruction_noWarnings(t *testing.T) {
	content := []byte("# Rules\n\nUse conventional commits.\n\nKeep PRs small.\n")
	r := validate.Instruction(content, validate.DefaultWarnSizeKB)
	if r.SizeWarning {
		t.Error("unexpected size warning for small file")
	}
	if len(r.DuplicateBlocks) != 0 {
		t.Errorf("unexpected duplicates: %v", r.DuplicateBlocks)
	}
	if r.HasWarnings() {
		t.Error("HasWarnings should be false")
	}
}

func TestInstruction_sizeWarning(t *testing.T) {
	// Build content just over the threshold using a custom 1 KB limit.
	line := strings.Repeat("x", 80) + "\n"
	content := []byte(strings.Repeat(line, 14)) // ~1.1 KB
	r := validate.Instruction(content, 1)
	if !r.SizeWarning {
		t.Errorf("expected size warning for %d bytes", len(content))
	}
}

func TestInstruction_belowCustomThreshold(t *testing.T) {
	content := []byte("# Rules\n\nUse conventional commits.\n")
	r := validate.Instruction(content, 96) // 96 KB limit — tiny file, no warning
	if r.SizeWarning {
		t.Errorf("unexpected size warning for tiny file with 96 KB threshold")
	}
}

func TestInstruction_duplicateBlocks(t *testing.T) {
	content := []byte(
		"Use conventional commits.\n\n" +
			"Keep PRs small.\n\n" +
			"Use conventional commits.\n", // exact duplicate of first paragraph
	)
	r := validate.Instruction(content, validate.DefaultWarnSizeKB)
	if len(r.DuplicateBlocks) != 1 {
		t.Fatalf("expected 1 duplicate, got %d: %v", len(r.DuplicateBlocks), r.DuplicateBlocks)
	}
	if !strings.Contains(r.DuplicateBlocks[0], "use conventional commits") {
		t.Errorf("unexpected duplicate preview: %q", r.DuplicateBlocks[0])
	}
}

func TestInstruction_duplicateNormalisedCase(t *testing.T) {
	// Paragraphs that differ only in case/whitespace should still be caught.
	content := []byte("Always use snake_case.\n\nAlways  USE  snake_case.\n")
	r := validate.Instruction(content, validate.DefaultWarnSizeKB)
	if len(r.DuplicateBlocks) != 1 {
		t.Fatalf("expected 1 duplicate after normalisation, got %d", len(r.DuplicateBlocks))
	}
}

func TestInstruction_duplicateReportedOnce(t *testing.T) {
	// A paragraph appearing three times should only be reported once.
	para := "Never break the build.\n"
	content := []byte(para + "\n" + para + "\n" + para)
	r := validate.Instruction(content, validate.DefaultWarnSizeKB)
	if len(r.DuplicateBlocks) != 1 {
		t.Errorf("expected 1 report for triple duplicate, got %d", len(r.DuplicateBlocks))
	}
}
