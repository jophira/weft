package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/instruction"
	"github.com/jophira/weft/internal/manifest"
)

// Instruction conflict detection extends ADR 0004 D5 to the one class it left
// out: a Tier B harness's managed instruction block. D5 hashes whole files, and
// a block is a fragment of a document the user owns, so file hashing is the
// wrong instrument (that is why ClassInstructions is excluded in
// internal/harness/conflict.go). But the fan-in is the same shape: two Tier B
// harnesses can both be edited since the last apply, and instructionWriteBack
// (cmd/profile.go) writes each source's section back to the source in a loop,
// so the second write silently discards the first (#257).
//
// Detection is keyed on the source section rather than the whole block, via
// instruction.ParseInline, so two harnesses editing different sources in the
// same window do not collide (see conflict-detection-class-coverage.md, Part 2
// option B).

// instructionDivergence is one Tier B harness's edit to one source's section,
// found by comparing its current content against the base weft last staged
// for that source.
type instructionDivergence struct {
	Harness   string
	Content   string
	AppliedAt time.Time
}

// instructionConflict is two or more Tier B harnesses diverging on the same
// source's instruction section since the last apply.
type instructionConflict struct {
	Source   string
	Diverged []instructionDivergence
}

func (c instructionConflict) harnesses() []string {
	names := make([]string, len(c.Diverged))
	for i, d := range c.Diverged {
		names[i] = d.Harness
	}
	return names
}

// since picks the oldest recorded apply among the diverged harnesses, for the
// same reason harness.Conflict does: it is the one timestamp the report can
// name that is true of all of them.
func (c instructionConflict) since() time.Time {
	var earliest time.Time
	for _, d := range c.Diverged {
		if d.AppliedAt.IsZero() {
			continue
		}
		if earliest.IsZero() || d.AppliedAt.Before(earliest) {
			earliest = d.AppliedAt
		}
	}
	return earliest
}

// report renders the user-facing conflict message, matching D5's format with
// the instructions:<source> path `weft resolve` will need.
func (c instructionConflict) report(now time.Time) string {
	names := c.harnesses()
	return fmt.Sprintf("! conflict: instructions (%s) changed in %s since %s\n  → weft resolve instructions:%s --take %s\n",
		c.Source, harness.JoinAnd(names), harness.SinceLabel(c.since(), now), c.Source, strings.Join(names, "|"))
}

// heldInstructions is what instructionConflicts freezes: the source names that
// must not be written back, and the exact content each diverging harness must
// keep in its own copy so reprojection does not silently revert it once the
// source stays put.
type heldInstructions struct {
	sources map[string]bool
	content map[string]map[string]string // harness -> source -> content to preserve
}

// detectInstructionConflicts finds every source whose instruction section two
// or more Tier B harnesses have both edited since the last apply.
//
// It must run before instructionWriteBack fans the first edit into the source
// and the second overwrites it — by then there is only one edit left to find,
// and detection would truthfully report no conflict. instrDir is read as a
// base, not a target: at the point this runs in mergeAndApply it still holds
// the previous apply's staged copies, because stageInstructions has not yet
// rewritten it for this apply.
func detectInstructionConflicts(targets []string, hReg *harness.Registry, cfgDir, instrDir string) ([]instructionConflict, error) {
	sections := map[string][]instructionDivergence{}
	var order []string

	for _, target := range targets {
		h, ok := hReg.Get(target)
		if !ok {
			continue
		}
		ic, ok := h.(harness.InstructionConsumer)
		if !ok {
			continue
		}
		spec, err := ic.InstructionSpec()
		if err != nil {
			continue // not resolvable (e.g. harness not installed) — nothing to compare
		}
		if spec.Strategy != harness.StrategyInline {
			continue // Tier A carries import directives only — no content to diverge
		}
		data, err := os.ReadFile(spec.Path) //nolint:gosec // spec.Path is the harness's own resolved instruction file
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", spec.Path, err)
		}
		body, found := instruction.Extract(data)
		if !found {
			continue
		}
		m, err := manifest.Load(cfgDir, h.Name())
		if err != nil {
			return nil, err
		}
		if m.InstructionBlock == "" {
			continue // never applied — no base to diverge from
		}

		for _, sec := range instruction.ParseInline(body) {
			if !instrSectionChanged(instrDir, sec) {
				continue // unchanged by this harness
			}
			if _, seen := sections[sec.Name]; !seen {
				order = append(order, sec.Name)
			}
			sections[sec.Name] = append(sections[sec.Name], instructionDivergence{
				Harness: h.Name(), Content: sec.Content, AppliedAt: m.AppliedAt,
			})
		}
	}

	var out []instructionConflict
	for _, name := range order {
		diverged := sections[name]
		// One diverged copy is the ordinary write-back case, not a conflict:
		// there is exactly one edit, so there is nothing to choose between.
		if len(diverged) < 2 {
			continue
		}
		sort.Slice(diverged, func(i, j int) bool { return diverged[i].Harness < diverged[j].Harness })
		out = append(out, instructionConflict{Source: name, Diverged: diverged})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out, nil
}

// instrSectionChanged reports whether sec's content differs from the base
// weft last staged for that source. A missing base (e.g. right after a
// profile switch, before #257) means there is nothing to compare against, so
// it conservatively reports changed — the pre-#257 default of always writing
// the section back, rather than a false negative that drops an edit.
//
// Both instructionWriteBack and detectInstructionConflicts must agree on this:
// write-back only pushes a section a harness actually touched, and detection
// only flags a source two or more harnesses actually touched. A harness whose
// current text for an untouched section still equals the base can otherwise
// silently clobber another harness's just-written edit to that same section —
// the write-back loop visits every harness whose block changed *somewhere*,
// not just the one with the relevant section's edit.
func instrSectionChanged(instrDir string, sec instruction.SourceContent) bool {
	base, ok := readInstrBase(instrDir, sec.Name)
	if !ok {
		return true
	}
	return strings.TrimSpace(sec.Content) != strings.TrimSpace(base)
}

// readInstrBase reads the staged per-source instruction copy weft last
// projected for sourceName, tolerating a shifted priority ordinal: the file
// is named "%02d-<name>.md" and the ordinal can move between applies.
func readInstrBase(instrDir, sourceName string) (string, bool) {
	matches, err := filepath.Glob(filepath.Join(instrDir, "*-"+sourceName+".md"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	data, err := os.ReadFile(matches[0]) //nolint:gosec // instrDir is weft's own config dir
	if err != nil {
		return "", false
	}
	return string(data), true
}

// holdInstructions builds the freeze set a set of conflicts implies: sources
// write-back must skip, and per-harness content reprojection must preserve
// verbatim rather than silently reverting to the unedited base.
func holdInstructions(conflicts []instructionConflict) heldInstructions {
	held := heldInstructions{sources: map[string]bool{}, content: map[string]map[string]string{}}
	for _, c := range conflicts {
		held.sources[c.Source] = true
		for _, d := range c.Diverged {
			if held.content[d.Harness] == nil {
				held.content[d.Harness] = map[string]string{}
			}
			held.content[d.Harness][c.Source] = d.Content
		}
	}
	return held
}

// overrideHeldSections returns sources with any held content substituted in
// for the given harness, so reprojecting that harness's block reproduces what
// it already has on disk instead of the regenerated (unedited) content —
// which is what the source still holds while the conflict is unresolved.
func overrideHeldSections(sources []harness.SourceInstruction, overrides map[string]string) []harness.SourceInstruction {
	if len(overrides) == 0 {
		return sources
	}
	out := make([]harness.SourceInstruction, len(sources))
	copy(out, sources)
	for i, s := range out {
		if content, ok := overrides[s.Name]; ok {
			out[i].Content = content
		}
	}
	return out
}
