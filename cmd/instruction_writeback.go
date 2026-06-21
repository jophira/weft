package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/instruction"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

// instructionWriteBack detects external edits to a Tier B harness's managed
// instruction block and writes each source's section back to its owning flat
// source's CLAUDE.md. Tier A (import) blocks carry only import directives — no
// content — so they are a no-op here. Hierarchical (glob) sources are skipped
// because their assembled content cannot be safely decomposed into one file.
//
// It is the cross-harness sync hub: an edit made in one harness lands in the
// source and is re-projected to every harness on the next apply.
func instructionWriteBack(h harness.Harness, cfgDir string, p *profile.Profile, srcs []source.Source) error {
	ic, ok := h.(harness.InstructionConsumer)
	if !ok {
		return nil
	}
	spec, err := ic.InstructionSpec()
	if err != nil {
		return nil //nolint:nilerr // harness not resolvable (e.g. not installed) — skip silently
	}
	if spec.Strategy != harness.StrategyInline {
		return nil // import block holds no content to write back
	}

	data, err := os.ReadFile(spec.Path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", spec.Path, err)
	}

	body, found := instruction.Extract(data)
	if !found {
		return nil
	}

	m, err := manifest.Load(cfgDir, h.Name())
	if err != nil {
		return err
	}
	// No external edit if the on-disk block matches what weft last wrote.
	if m.InstructionBlock == "" || manifest.HashBytes([]byte(body)) == m.InstructionBlock {
		return nil
	}

	srcMap := buildSrcMap(srcs)
	_ = p // reserved for future write_back overrides; sections route by source name today
	for _, sec := range instruction.ParseInline(body) {
		s, ok := srcMap[sec.Name]
		if !ok {
			continue // unknown source — leave the edit in place
		}
		if glob := s.Structure.InstructionGlob; glob != "" && glob != "CLAUDE.md" {
			fmt.Printf("[weft] skip instruction write-back for %q — hierarchical source\n", sec.Name)
			continue
		}
		dst := filepath.Join(locate.ExpandHome(s.Root), "CLAUDE.md")
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
			return fmt.Errorf("creating source dir for %q: %w", sec.Name, mkErr)
		}
		out := normalizeForSource([]byte(sec.Content + "\n"))
		if wErr := os.WriteFile(dst, out, 0o644); wErr != nil { //nolint:gosec // dst derived from registered source root
			return fmt.Errorf("writing instruction write-back to source %q: %w", sec.Name, wErr)
		}
		fmt.Printf("[weft] instruction write-back: %s → %s\n", h.Name(), sec.Name)
	}
	return nil
}
