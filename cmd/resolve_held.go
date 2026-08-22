package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/merge"
)

// One shape for both kinds of held conflict.
//
// A file two harnesses both edited and an instruction section two harnesses
// both edited are the same disagreement wearing different clothes: several
// versions of one piece of text, one base weft last wrote, and a user who has
// to say which survives. `weft resolve` walks them in one list (#260), so they
// need one shape to be walked in.

// heldConflict is one conflict as the resolver sees it, whatever class it came
// from.
type heldConflict struct {
	// Label is what the user types: a staged-relative path for a file, or
	// "instructions:<source>" for a Tier B instruction section.
	Label     string
	Harnesses []string
	Since     time.Time
	// Base is what weft last projected, and the third input a merge needs.
	// HasBase records whether one was found at all, separately from whether it
	// had any content: a file weft last wrote as empty is a perfectly good base to
	// merge two additions against, while a base that is simply gone — a profile
	// switch clears the staged instruction copies — means merging is refused and
	// --take is the only way through.
	Base    string
	HasBase bool
	Sides   map[string]string // harness -> the text it currently holds

	// settle writes the resolution. Exactly one of winner and merged is
	// meaningful: a winner names the harness whose copy survives, merged carries
	// text a human has already reviewed.
	settle func(winner string, merged []byte) (settleReport, error)
}

// settleReport is what a resolution did, in the terms the summary prints.
type settleReport struct {
	Winner     string   // harness name, or "merge"
	Rewritten  []string // harnesses brought into line
	BackupDir  string
	SourceName string
	SourcePath string
}

// instructionsPrefix marks a label that names a source's instruction section
// rather than a file. Instruction content is a fragment of a document the user
// owns, so it has no path of its own to be named by.
const instructionsPrefix = "instructions:"

// collectHeldConflicts re-detects every conflict weft is currently holding,
// across both classes, in the order `weft resolve` should walk them.
//
// Detection is repeated here rather than read from a stored record: the user may
// have edited a third harness, or fixed the divergence by hand, since the report
// was printed. Acting on a stale record would rewrite files over a disagreement
// that no longer exists.
func collectHeldConflicts(cfgDir, profileName string) ([]heldConflict, error) {
	p, _, srcs, err := resolveProfileRoots(profileName)
	if err != nil {
		return nil, err
	}

	stagedDir := filepath.Join(cfgDir, "staged", profileName)
	targets, err := conflictTargets(cfgDir)
	if err != nil {
		return nil, err
	}
	fileConflicts, err := harness.DetectConflicts(stagedDir, targets)
	if err != nil {
		return nil, err
	}

	held := make([]heldConflict, 0, len(fileConflicts))
	for _, c := range fileConflicts {
		srcPath, srcName := resolveConflictSource(c.Canonical, p, srcs)
		sides := make(map[string]string, len(c.Diverged))
		for _, d := range c.Diverged {
			data, rErr := os.ReadFile(d.Abs) //nolint:gosec // Abs was resolved from a harness root during detection
			if rErr != nil {
				return nil, fmt.Errorf("reading the %s copy of %s: %w", d.Harness, c.Canonical, rErr)
			}
			sides[d.Harness] = string(data)
		}
		base, bErr := os.ReadFile(filepath.Join(stagedDir, filepath.FromSlash(c.Canonical))) //nolint:gosec // path under weft's own config dir
		held = append(held, heldConflict{
			Label:     c.Canonical,
			Harnesses: c.Harnesses(),
			Since:     c.Since,
			Base:      string(base),
			HasBase:   bErr == nil,
			Sides:     sides,
			settle: func(winner string, merged []byte) (settleReport, error) {
				res, rErr := harness.Resolve(harness.ResolveRequest{
					Conflict: c, Take: winner, Merged: merged, SourcePath: srcPath, CfgDir: cfgDir,
				})
				if rErr != nil {
					return settleReport{}, rErr
				}
				rewritten := make([]string, len(res.Rewritten))
				for i, d := range res.Rewritten {
					rewritten[i] = d.Harness
				}
				return settleReport{
					Winner: res.Winner, Rewritten: rewritten, BackupDir: res.BackupDir,
					SourceName: srcName, SourcePath: res.SourcePath,
				}, nil
			},
		})
	}

	instrHeld, err := collectInstructionHeld(cfgDir, p, srcs)
	if err != nil {
		return nil, err
	}
	return append(held, instrHeld...), nil
}

// findHeld picks the conflict a label names, or explains what is actually held
// so a typo can be corrected without re-running the apply.
func findHeld(held []heldConflict, label string) (heldConflict, error) {
	want := label
	if !strings.HasPrefix(want, instructionsPrefix) {
		want = filepath.ToSlash(filepath.Clean(want))
	}
	for _, h := range held {
		if h.Label == want {
			return h, nil
		}
	}
	if len(held) == 0 {
		return heldConflict{}, fmt.Errorf("no conflicts to resolve — nothing is held")
	}
	labels := make([]string, len(held))
	for i, h := range held {
		labels[i] = h.Label
	}
	return heldConflict{}, fmt.Errorf("%s is not in conflict — weft is holding %s", want, strings.Join(labels, ", "))
}

// fingerprint digests exactly what a merge was computed from: the base and every
// side's text. A reviewed file carries it, so weft can tell whether the state it
// was merged out of is still the state on disk.
func (h heldConflict) fingerprint() string {
	var b strings.Builder
	b.WriteString(h.Base)
	for _, name := range sortedHarnesses(h) {
		b.WriteString("\x00" + name + "\x00" + h.Sides[name])
	}
	return manifest.HashBytes([]byte(b.String()))
}

// sameAs reports whether two detections of one conflict describe the same
// disagreement: the same harnesses, holding the same text, over the same base.
func (h heldConflict) sameAs(other heldConflict) bool {
	if len(h.Sides) != len(other.Sides) || h.Base != other.Base || h.HasBase != other.HasBase {
		return false
	}
	for name, text := range h.Sides {
		if other.Sides[name] != text {
			return false
		}
	}
	return true
}

// confirmUnchanged re-detects one conflict and reports whether it still looks
// the way it did when it was read.
//
// Merging waits on a human in an editor, and an editor session has no bound.
// A harness writing to the same file during that window would otherwise be
// overwritten by the reviewed text and have its manifest hash reset to match,
// which is exactly the silent loss of one of two edits that conflict detection
// exists to prevent. Re-reading costs one scan after a wait measured in minutes.
func confirmUnchanged(cfgDir, profileName string, h heldConflict) error {
	held, err := collectHeldConflicts(cfgDir, profileName)
	if err != nil {
		return fmt.Errorf("re-checking %s after the review: %w", h.Label, err)
	}
	current, err := findHeld(held, h.Label)
	if err != nil {
		return fmt.Errorf("%s changed while the merge was open, so the review no longer applies: %w", h.Label, err)
	}
	if !h.sameAs(current) {
		return fmt.Errorf(
			"%s changed while the merge was open — a harness wrote to it during the review, so applying "+
				"the merge would discard that edit; re-run the resolve to merge against what is there now", h.Label)
	}
	return nil
}

// settleHeld applies one resolution and reports it. take names a harness, or
// "merge", in which case merged carries the reviewed text.
func settleHeld(out io.Writer, h heldConflict, take string, merged []byte) error {
	if merged != nil && merge.HasConflictMarkers(string(merged)) {
		return harness.ErrConflictMarkers
	}
	res, err := h.settle(take, merged)
	if err != nil {
		return err
	}
	if merged != nil {
		fmt.Fprintf(out, "✓ %s resolved — merged, as reviewed\n", h.Label)
	} else {
		fmt.Fprintf(out, "✓ %s resolved — took the %s copy\n", h.Label, res.Winner)
	}
	for _, name := range res.Rewritten {
		fmt.Fprintf(out, "  ✓ %s ← %s\n", name, res.Winner)
	}
	if res.BackupDir != "" {
		fmt.Fprintf(out, "  previous copies kept in %s\n", locate.Tilde(res.BackupDir))
	}
	if res.SourceName != "" {
		fmt.Fprintf(out, "  source %q updated (%s)\n", res.SourceName, locate.Tilde(res.SourcePath))
	} else {
		fmt.Fprintln(out, "  no owning source found — the harness copies agree, but the next apply will "+
			"restore what the source holds; set write_back.default in your profile")
	}
	return nil
}
