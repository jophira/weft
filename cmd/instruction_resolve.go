package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jophira/weft/internal/anchor"
	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/instruction"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

// Resolving an instruction conflict, as opposed to a file one.
//
// A section is not a file, so none of harness.Resolve applies: there is no path
// to back up, no copy to overwrite wholesale, and the winning text has to be
// edited into a block that also holds other sources' sections and the user's own
// prose around it. What is the same is the shape of the settlement — back the
// losing copies up, bring every diverged harness into line, write the result to
// the owning source so the next apply re-projects the resolution rather than
// undoing it.

// collectInstructionHeld re-detects the instruction sections two or more Tier B
// harnesses have both edited, and returns them in the resolver's shape.
//
// The base each side is compared against is the staged per-source copy under
// profiles/<profile>/instructions/. At this point — after an apply, before the
// next one — that copy holds what weft last projected, which is what both a
// change check and a three-way merge need.
func collectInstructionHeld(cfgDir string, p *profile.Profile, srcs []source.Source) ([]heldConflict, error) {
	hReg := harness.NewRegistry(harness.Instances()...)
	targets := resolveApplyTargets(p, true)
	instrDir := filepath.Join(cfgDir, "profiles", p.Name, "instructions")

	conflicts, err := detectInstructionConflicts(targets, hReg, cfgDir, instrDir)
	if err != nil {
		return nil, err
	}

	held := make([]heldConflict, 0, len(conflicts))
	for _, c := range conflicts {
		base, hasBase := readInstrBase(instrDir, c.Source)
		sides := make(map[string]string, len(c.Diverged))
		for _, d := range c.Diverged {
			sides[d.Harness] = d.Content
		}
		held = append(held, heldConflict{
			Label:     instructionsPrefix + c.Source,
			Harnesses: c.harnesses(),
			Since:     c.since(),
			Base:      base,
			HasBase:   hasBase,
			Sides:     sides,
			settle: func(winner string, merged []byte) (settleReport, error) {
				return settleInstructionConflict(c, winner, merged, hReg, cfgDir, instrDir, srcs)
			},
		})
	}
	return held, nil
}

// settleInstructionConflict writes one resolution across all three places the
// section lives: every diverged harness's block, the staged base copy, and the
// owning source.
//
// All three matter. Skipping the harness blocks leaves them diverged from the
// new base and the same conflict is reported again on the next apply; skipping
// the source lets the next apply re-project the pre-conflict text over the
// resolution.
func settleInstructionConflict(
	c instructionConflict, winner string, merged []byte,
	hReg *harness.Registry, cfgDir, instrDir string, srcs []source.Source,
) (settleReport, error) {
	resolved, keep, err := instructionWinner(c, winner, merged)
	if err != nil {
		return settleReport{}, err
	}

	backupDir, err := backupInstructionSections(cfgDir, c, keep)
	if err != nil {
		return settleReport{}, err
	}

	rewritten := make([]string, 0, len(c.Diverged))
	for _, d := range c.Diverged {
		if d.Harness == keep {
			continue
		}
		if wErr := rewriteHarnessSection(hReg, cfgDir, d.Harness, c.Source, resolved); wErr != nil {
			return settleReport{}, wErr
		}
		rewritten = append(rewritten, d.Harness)
	}

	// The base moves with the resolution. Leaving it behind would make every
	// harness that now holds the resolved text look edited on the next scan.
	if wErr := writeInstrBase(instrDir, c.Source, resolved); wErr != nil {
		return settleReport{}, wErr
	}

	srcName, srcPath, err := writeInstructionSource(c.Source, resolved, srcs)
	if err != nil {
		return settleReport{}, err
	}
	reported := keep
	if reported == "" {
		reported = mergeWinner
	}
	return settleReport{
		Winner: reported, Rewritten: rewritten, BackupDir: backupDir,
		SourceName: srcName, SourcePath: srcPath,
	}, nil
}

// mergeWinner is what a merged resolution reports in place of a harness name.
// It matches what harness.Resolve reports for the file class.
const mergeWinner = "merge"

// instructionWinner picks the text the resolution writes and, for a --take, the
// harness whose copy is already correct and therefore needs no rewrite. A merge
// keeps nobody: the text came from all of them, so every copy is replaced.
func instructionWinner(c instructionConflict, winner string, merged []byte) (resolved, keep string, err error) {
	if merged != nil {
		return string(merged), "", nil
	}
	for _, d := range c.Diverged {
		if d.Harness == winner {
			return d.Content, d.Harness, nil
		}
	}
	return "", "", fmt.Errorf(
		"%q is not one of the harnesses that changed the %s instructions — pick one of %s",
		winner, c.Source, harness.JoinAnd(c.harnesses()))
}

// backupInstructionSections preserves each losing section as it stood, under the
// same backups/resolve/<ts>/ tree the file class writes to. The user chose a
// winner, not the destruction of the other side's text.
func backupInstructionSections(cfgDir string, c instructionConflict, keep string) (string, error) {
	dir := filepath.Join(cfgDir, "backups", "resolve", time.Now().Format("20060102-150405"))
	for _, d := range c.Diverged {
		if d.Harness == keep {
			continue
		}
		dst := filepath.Join(dir, d.Harness, "instructions", c.Source+".md")
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
			return "", fmt.Errorf("creating backup dir for %s: %w", d.Harness, mkErr)
		}
		if wErr := os.WriteFile(dst, []byte(d.Content+"\n"), 0o644); wErr != nil { //nolint:gosec // dst is under weft's own backup dir
			return "", fmt.Errorf("backing up the %s instructions for %q: %w", d.Harness, c.Source, wErr)
		}
	}
	return dir, nil
}

// rewriteHarnessSection replaces one source's section inside a harness's managed
// block, in place. The block also carries the other sources' sections and an
// advertised-index tail, and the file around it carries the user's own prose, so
// the section is edited rather than the block rebuilt.
func rewriteHarnessSection(hReg *harness.Registry, cfgDir, harnessName, sourceName, resolved string) error {
	h, ok := hReg.Get(harnessName)
	if !ok {
		return fmt.Errorf("unknown harness %q", harnessName)
	}
	ic, ok := h.(harness.InstructionConsumer)
	if !ok {
		return nil
	}
	spec, err := ic.InstructionSpec()
	if err != nil {
		return nil //nolint:nilerr // harness not resolvable (e.g. not installed) — nothing to rewrite
	}
	data, err := os.ReadFile(spec.Path) //nolint:gosec // spec.Path is the harness's own resolved instruction file
	if err != nil {
		return fmt.Errorf("reading %s: %w", spec.Path, err)
	}
	body, found := instruction.Extract(data)
	if !found {
		return fmt.Errorf("no weft block in %s — re-apply the profile before resolving", spec.Path)
	}
	newBody, replaced := instruction.ReplaceSection(body, sourceName, resolved)
	if !replaced {
		return fmt.Errorf("no %q section in %s — re-apply the profile before resolving", sourceName, spec.Path)
	}
	updated := instruction.Upsert(data, newBody)
	if wErr := os.WriteFile(spec.Path, updated, 0o644); wErr != nil { //nolint:gosec // path resolved from harness config, not user input
		return fmt.Errorf("writing %s: %w", spec.Path, wErr)
	}

	m, err := manifest.Load(cfgDir, harnessName)
	if err != nil {
		return err
	}
	// Hash what Extract reads back, matching ProjectInstruction: a stale hash
	// here would register the resolution itself as an external edit.
	roundTripped, _ := instruction.Extract(updated)
	m.InstructionBlock = manifest.HashBytes([]byte(roundTripped))
	return manifest.Save(cfgDir, m)
}

// writeInstrBase updates the staged per-source copy readInstrBase reads. The
// copies are written read-only (#261), so the mode is lifted for the write and
// restored after.
func writeInstrBase(instrDir, sourceName, resolved string) error {
	matches, err := filepath.Glob(filepath.Join(instrDir, "*-"+sourceName+".md"))
	if err != nil || len(matches) == 0 {
		return nil // no base staged (e.g. right after a profile switch) — nothing to keep in step
	}
	path := matches[0]
	if cErr := os.Chmod(path, 0o644); cErr != nil {
		return fmt.Errorf("unlocking the staged copy of %q: %w", sourceName, cErr)
	}
	if wErr := os.WriteFile(path, []byte(resolved+"\n"), 0o644); wErr != nil { //nolint:gosec // path under weft's own config dir
		return fmt.Errorf("updating the staged copy of %q: %w", sourceName, wErr)
	}
	return os.Chmod(path, 0o444)
}

// writeInstructionSource writes the resolved section back to its owning source,
// through the same normalisation instructionWriteBack uses: placeholder blocks
// collapse back to their compact form, attribution and generated-file markers
// come out, and expanded paths collapse back to weft anchors so the source stays
// portable (#259).
func writeInstructionSource(sourceName, resolved string, srcs []source.Source) (name, path string, err error) {
	srcMap := buildSrcMap(srcs)
	s, ok := srcMap[sourceName]
	if !ok {
		return "", "", nil
	}
	if glob := s.Structure.InstructionGlob; glob != "" && glob != "CLAUDE.md" {
		return "", "", fmt.Errorf(
			"source %q is hierarchical — its assembled instructions cannot be written back to one file; "+
				"edit the source files directly", sourceName)
	}
	dst := filepath.Join(s.Root, "CLAUDE.md")
	if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
		return "", "", fmt.Errorf("creating source dir for %q: %w", sourceName, mkErr)
	}
	out := anchor.Collapse(normalizeForSource([]byte(resolved+"\n")), anchorsForSource(sourceName, srcMap))
	if wErr := os.WriteFile(dst, out, 0o644); wErr != nil { //nolint:gosec // dst derived from registered source root
		return "", "", fmt.Errorf("writing the resolved instructions to source %q: %w", sourceName, wErr)
	}
	return sourceName, dst, nil
}
