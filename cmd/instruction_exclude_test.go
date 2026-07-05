package cmd

import (
	"strings"
	"testing"

	"github.com/jophira/weft/internal/source"
)

// TestBuildAssemblerHonoursInstructionExclude verifies that a broad glob
// combined with InstructionExclude assembles only the non-excluded subtree,
// leaving language/ticket trees out of the instruction (issue #168).
func TestBuildAssemblerHonoursInstructionExclude(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"CLAUDE.md":           "# root rules",
		"common/guide.md":     "COMMON_GUIDE",
		"java/springboot.md":  "JAVA_RULES",
		"tickets/DIGI-1/x.md": "TICKET_BODY",
		"commands/ship.md":    "SHIP_CMD",
	})

	s := source.Source{
		Name: "work",
		Root: root,
		Structure: source.Structure{
			InstructionGlob:    "**/*.md",
			InstructionExclude: []string{"java/", "tickets/"},
			// commands/ is a managed dir and always excluded from assembly.
			Commands: "commands/",
		},
	}

	assembler := buildAssembler([]string{root}, []source.Source{s})
	out, err := assembler(root)
	if err != nil {
		t.Fatalf("assembler: %v", err)
	}
	got := string(out)

	mustContain := []string{"root rules", "COMMON_GUIDE"}
	mustOmit := []string{"JAVA_RULES", "TICKET_BODY", "SHIP_CMD"}
	for _, w := range mustContain {
		if !strings.Contains(got, w) {
			t.Errorf("assembled instruction missing %q\n---\n%s", w, got)
		}
	}
	for _, w := range mustOmit {
		if strings.Contains(got, w) {
			t.Errorf("assembled instruction should have excluded %q\n---\n%s", w, got)
		}
	}
}
