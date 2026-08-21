package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/pathlint"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

// TestWriteBack_RestoresAnchorInSource is the integration case #259 asks for:
// projection expands {{weft.root}} to an absolute path, and a write-back that
// never touches that line must not let the expanded, machine-specific path
// leak back into the source — it must collapse back to the anchor.
func TestWriteBack_RestoresAnchorInSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	personal := t.TempDir()
	writeFile(t, filepath.Join(personal, "CLAUDE.md"), "# personal rules\nSee {{weft.root}}/guide.md for details.\n")

	srcs := []source.Source{
		{Name: "personal", Root: personal, Priority: 10, Structure: source.DefaultStructure()},
	}
	p := &profile.Profile{
		Name:    "single",
		Sources: []string{"personal"},
		Overlay: profile.OverlayCascade,
		Targets: []string{"codex"},
	}

	if err := mergeAndApply(p, []string{personal}, srcs, cfgDir, false); err != nil {
		t.Fatalf("initial mergeAndApply: %v", err)
	}

	agentsPath := filepath.Join(home, ".codex", "AGENTS.md")
	agents := readFile(t, agentsPath)
	if !strings.Contains(agents, personal+"/guide.md") {
		t.Fatalf("expected the anchor expanded to the source root in the projected file:\n%s", agents)
	}
	if strings.Contains(agents, "{{weft.root}}") {
		t.Fatalf("projected file should not still carry the anchor token:\n%s", agents)
	}

	// Edit a line that has nothing to do with the anchor — the anchor line
	// itself is untouched, exactly as projection left it (expanded).
	edited := strings.Replace(agents, "# personal rules", "# personal rules\nnew line from codex", 1)
	writeFile(t, agentsPath, edited)

	if err := mergeAndApply(p, []string{personal}, srcs, cfgDir, false); err != nil {
		t.Fatalf("re-apply mergeAndApply: %v", err)
	}

	personalSrc := readFile(t, filepath.Join(personal, "CLAUDE.md"))
	if !strings.Contains(personalSrc, "{{weft.root}}/guide.md") {
		t.Errorf("write-back should have restored the anchor in the source, got:\n%s", personalSrc)
	}
	if strings.Contains(personalSrc, personal+"/guide.md") {
		t.Errorf("write-back left a hardcoded, machine-specific path in the source:\n%s", personalSrc)
	}

	findings, err := pathlint.Scan([]pathlint.Source{{Name: "personal", Root: personal}})
	if err != nil {
		t.Fatalf("pathlint.Scan: %v", err)
	}
	for _, f := range findings {
		if f.Kind == pathlint.HardcodedInSource {
			t.Errorf("weft doctor should report no hardcoded-in-source findings after write-back, got: %+v", f)
		}
	}
}
