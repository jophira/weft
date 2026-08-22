package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jophira/weft/internal/diff"
	"github.com/jophira/weft/internal/harness"
)

// Detection stays non-interactive; resolution may ask.
//
// Conflicts are detected on `weft profile use` and on every watcher tick, and a
// prompt on the watcher path hangs forever with nobody to answer it. So apply
// reports and holds, never asks. `weft resolve` may ask, because a human typed
// it — and only when stdin is a TTY and no --take was given. `--yes` or a pipe
// falls back to reporting and exiting non-zero, the same shape `weft adopt` has.
//
// Interactive is sugar. Each choice does exactly what the equivalent flag does.

// Reserved menu letters. Harness letters are assigned around these, so a harness
// called "merge" or "skip" cannot steal a fixed action.
const (
	keyMerge = "m"
	keyDiff  = "d"
	keySkip  = "s"
	keyQuit  = "q"
)

// errConflictsHeld reports that conflicts remain, so a non-interactive run exits
// non-zero without having asked anything.
var errConflictsHeld = errors.New("conflicts are held — settle them with 'weft resolve <path> --take <harness>'")

// resolveLoop walks every held conflict, one prompt at a time.
//
// Skipping leaves a conflict held, which is the safe default and lets someone
// settle two now and one tomorrow. Quitting leaves the remainder held for the
// same reason. Each settlement re-reads the files it touches, so resolving one
// conflict does not disturb the ones after it.
func resolveLoop(in io.Reader, out io.Writer, cfgDir, profileName string, held []heldConflict) error {
	sc := bufio.NewScanner(in)
	for i, h := range held {
		if err := resolveOne(sc, out, cfgDir, profileName, h, i+1, len(held)); err != nil {
			if errors.Is(err, errQuitLoop) {
				break
			}
			return err
		}
	}
	return nil
}

var errQuitLoop = errors.New("quit")

// resolveOne prompts for one conflict until a choice settles it, skips it, or
// quits the loop. A failed settlement re-prompts rather than aborting the walk:
// the other conflicts are unaffected by this one going wrong.
func resolveOne(sc *bufio.Scanner, out io.Writer, cfgDir, profileName string, h heldConflict, n, total int) error {
	keys := harnessKeys(h.Harnesses)
	now := time.Now()

	for {
		fmt.Fprintf(out, "\n! conflict %d/%d: %s\n    changed in %s since %s\n\n",
			n, total, h.Label, harness.JoinAnd(h.Harnesses), harness.SinceLabel(h.Since, now))
		fmt.Fprintf(out, "  [%s] merge both, and review in $EDITOR\n", keyMerge)
		for _, name := range sortedHarnesses(h) {
			fmt.Fprintf(out, "  [%s] take %s\n", keys[name], name)
		}
		fmt.Fprintf(out, "  [%s] show the diff\n  [%s] skip\n  [%s] quit\n", keyDiff, keySkip, keyQuit)
		fmt.Fprint(out, "> ")

		if !sc.Scan() {
			return errQuitLoop // stdin closed mid-walk: the rest stay held
		}
		choice := strings.ToLower(strings.TrimSpace(sc.Text()))

		switch choice {
		case keyQuit:
			return errQuitLoop
		case keySkip, "":
			fmt.Fprintf(out, "  skipped — %s stays held\n", h.Label)
			return nil
		case keyDiff:
			printSideDiffs(out, h)
			continue
		case keyMerge:
			merged, err := mergeAndReview(out, cfgDir, h, now)
			if err != nil {
				reportChoiceError(out, h, err)
				return nil
			}
			if cErr := confirmUnchanged(cfgDir, profileName, h); cErr != nil {
				reportChoiceError(out, h, cErr)
				return nil
			}
			if sErr := settleHeld(out, h, mergeWinner, merged); sErr != nil {
				reportChoiceError(out, h, sErr)
				return nil
			}
			return nil
		}

		name, ok := harnessForKey(keys, choice)
		if !ok {
			fmt.Fprintf(out, "  %q is not one of the choices\n", choice)
			continue
		}
		if err := settleHeld(out, h, name, nil); err != nil {
			reportChoiceError(out, h, err)
		}
		return nil
	}
}

// reportChoiceError prints why a choice did not settle and says what that leaves
// behind, rather than failing the whole walk over one conflict.
func reportChoiceError(out io.Writer, h heldConflict, err error) {
	if errors.Is(err, errNoEditor) {
		return // reviewInEditor already printed the path and the command
	}
	fmt.Fprintf(out, "  %v\n  %s stays held\n", err, h.Label)
}

// printSideDiffs shows each harness's copy against the base, rather than one
// side against another. With three harnesses on one file there is no single
// pair to show, and the base is the thing every side actually diverged from.
func printSideDiffs(out io.Writer, h heldConflict) {
	for _, name := range sortedHarnesses(h) {
		fmt.Fprintf(out, "\n  ── %s vs what weft last wrote ──\n", name)
		if !h.HasBase {
			fmt.Fprintln(out, "  (no base on disk — nothing to compare against)")
			continue
		}
		fmt.Fprintln(out, diff.LineDiff(h.Base, h.Sides[name]))
	}
}

// harnessKeys binds a menu letter to each harness name. Letters come from the
// name itself so the binding explains itself, and fall back to digits when a
// name shares every letter with one already bound. The set of harnesses is
// variable, so fixed keys are not an option.
func harnessKeys(names []string) map[string]string {
	taken := map[string]bool{keyMerge: true, keyDiff: true, keySkip: true, keyQuit: true}
	keys := make(map[string]string, len(names))

	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for i, name := range sorted {
		key := ""
		for _, r := range strings.ToLower(name) {
			c := string(r)
			if c >= "a" && c <= "z" && !taken[c] {
				key = c
				break
			}
		}
		if key == "" {
			key = fmt.Sprintf("%d", i+1)
		}
		taken[key] = true
		keys[name] = key
	}
	return keys
}

func harnessForKey(keys map[string]string, choice string) (string, bool) {
	for name, key := range keys {
		if key == choice {
			return name, true
		}
	}
	return "", false
}
