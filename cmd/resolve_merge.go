package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/merge"
)

// A merge is never applied silently.
//
// The risk with merging is not a merge that fails. It is a clean merge that is
// semantically wrong: two harnesses each add a rule, in different places, that
// contradict each other. The text merges without a marker, weft reports nothing,
// and the model reads both on the next turn. So review is least skippable
// exactly when the merge succeeds — a merge with markers already forces
// attention; a clean one is the one that slips past.
//
// Hence: merge writes the result to a work file, opens $EDITOR on it, and the
// saved file is the resolution. Nothing reaches a source or a harness until the
// editor closes. There is no --dry-run, because the editor is the preview.

var (
	// errMergeAbandoned is the ordinary outcome of quitting the editor without
	// saving. The conflict stays held, both copies stay where the user left them.
	errMergeAbandoned = errors.New("merge abandoned — the editor exited without saving")

	// errNoEditor stands for "the merge is written, review it yourself". Its
	// message is the two printed lines above it, so callers stay quiet on it.
	errNoEditor = errors.New("no editor configured")

	// errMergeNeedsTTY refuses an unattended merge. `--take <harness>` stays
	// fully non-interactive for scripts; merging is the operation this file
	// argues must not run with nobody watching.
	errMergeNeedsTTY = errors.New(
		"--take merge needs review, so it must run interactively — " +
			"run 'weft resolve' from a terminal, or pick a side with --take <harness>")

	// errNoMergeBase fires when weft cannot say what it last projected, which is
	// the third input a three-way merge needs. A profile switch clears the staged
	// instruction copies, so this is reachable in normal use.
	errNoMergeBase = errors.New(
		"no base to merge from — weft cannot tell what it last wrote here " +
			"(a profile switch clears it); pick a side with --take <harness>")
)

// Provenance sits in a comment block at the top of the work file rather than in
// a diff. A diff is not editable as a result, so the editor has to open the
// merge output itself; the header carries the information the diff would have.
// It is stripped on save, and the markers are unambiguous enough to strip
// exactly.
const (
	mergeHeaderBegin = "<!-- weft:merge:begin"
	mergeHeaderEnd   = "weft:merge:end -->"
	// mergeFromKey stamps what the merge was computed from. A reviewed file can
	// sit on disk indefinitely when there is no $EDITOR to open, and applying one
	// that was merged out of text a harness has since replaced would discard that
	// replacement — the loss conflict detection exists to prevent, arrived at
	// through the back door.
	mergeFromKey = "weft:merge:from "
)

// mergeFingerprint reads the stamp back out of a reviewed file. A file with no
// stamp is taken at face value: the user may have assembled it themselves, and
// refusing text they wrote by hand would be worse than trusting it.
func mergeFingerprint(s string) (string, bool) {
	i := strings.Index(s, mergeFromKey)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(mergeFromKey):]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	return strings.TrimSpace(rest), true
}

// mergeWorkFile is where the merge result waits for review. It lives under the
// config dir rather than a temp dir so the path printed when $EDITOR is unset is
// still there when the user gets to it, and it is named after the conflict so a
// second merge overwrites its own file rather than accumulating.
func mergeWorkFile(cfgDir, label string) string {
	slug := strings.NewReplacer("/", "-", ":", "-", string(filepath.Separator), "-").Replace(label)
	if !strings.HasSuffix(slug, ".md") {
		slug += ".md"
	}
	return filepath.Join(cfgDir, "merge", slug)
}

// mergeAndReview merges every side against the base, puts the result in front of
// the user, and returns the text they saved.
func mergeAndReview(out io.Writer, cfgDir string, h heldConflict, now time.Time) ([]byte, error) {
	if !h.HasBase {
		return nil, errNoMergeBase
	}
	sides := make([]merge.Side, 0, len(h.Harnesses))
	for _, name := range sortedHarnesses(h) {
		sides = append(sides, merge.Side{Label: name, Text: h.Sides[name]})
	}
	merged, conflicted := merge.ThreeWay(h.Base, sides)

	path := mergeWorkFile(cfgDir, h.Label)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating the merge work dir: %w", err)
	}
	body := mergeHeader(h, conflicted, now) + ensureTrailingNL(merged)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // path under weft's own config dir
		return nil, fmt.Errorf("writing the merge for review: %w", err)
	}

	edited, err := reviewInEditor(out, path, h.Label)
	if err != nil {
		return nil, err
	}
	reviewed := strings.TrimLeft(stripMergeHeader(string(edited)), "\n")
	if merge.HasConflictMarkers(reviewed) {
		return nil, fmt.Errorf("%w (work file kept at %s)", harness.ErrConflictMarkers, locate.Tilde(path))
	}
	return []byte(reviewed), nil
}

// sortedHarnesses orders the sides deterministically, so the same conflict
// merges to the same text on every run and a marked block always names the
// harnesses in the same order.
func sortedHarnesses(h heldConflict) []string {
	names := append([]string(nil), h.Harnesses...)
	sort.Strings(names)
	return names
}

// mergeHeader renders the provenance block: what is being merged, which
// harnesses changed it and since when, and what saving or quitting will do.
func mergeHeader(h heldConflict, conflicted bool, now time.Time) string {
	var b strings.Builder
	b.WriteString(mergeHeaderBegin + "\n")
	fmt.Fprintf(&b, "  %s — changed in %s since %s\n",
		h.Label, harness.JoinAnd(h.Harnesses), harness.SinceLabel(h.Since, now))
	for _, name := range sortedHarnesses(h) {
		fmt.Fprintf(&b, "  %s: %d line(s)\n", name, strings.Count(ensureTrailingNL(h.Sides[name]), "\n"))
	}
	if conflicted {
		b.WriteString("  Overlapping edits are marked below. Remove every marker before saving.\n")
	} else {
		b.WriteString("  The edits did not overlap and were combined below. Read them: a clean merge can\n" +
			"  still hold two rules that contradict each other.\n")
	}
	b.WriteString("  Save and exit to apply this text. Exit without saving to leave the conflict held.\n")
	b.WriteString("  This header is removed on save.\n")
	b.WriteString("  " + mergeFromKey + h.fingerprint() + "\n")
	b.WriteString("  " + mergeHeaderEnd + "\n\n")
	return b.String()
}

// stripMergeHeader removes the provenance block, so it never reaches a source or
// a harness. Absent or hand-deleted, the text passes through unchanged.
func stripMergeHeader(s string) string {
	start := strings.Index(s, mergeHeaderBegin)
	if start < 0 {
		return s
	}
	end := strings.Index(s[start:], mergeHeaderEnd)
	if end < 0 {
		return s
	}
	end += start + len(mergeHeaderEnd)
	if nl := strings.IndexByte(s[end:], '\n'); nl >= 0 {
		end += nl + 1
	}
	return s[:start] + s[end:]
}

// reviewInEditor opens path in the user's editor and returns what they saved.
//
// An editor that exits without writing is the abandon signal, detected by
// modification time the way git detects an aborted commit message. With no
// editor configured the file and the command that consumes it are printed
// instead — still not silent, just not immediate.
func reviewInEditor(out io.Writer, path, label string) ([]byte, error) {
	editor := firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"))
	if strings.TrimSpace(editor) == "" {
		fmt.Fprintf(out, "  $EDITOR is unset. The merge is waiting for you at:\n    %s\n", locate.Tilde(path))
		fmt.Fprintf(out, "  Review it, then apply it with:\n    weft resolve %s --merged %s\n",
			label, locate.Tilde(path))
		return nil, errNoEditor
	}

	before, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	// Push the work file's timestamp back before handing it over. Weft wrote it a
	// moment ago, and on a filesystem with coarse timestamps an editor saving
	// within the same granule would land on the same mtime — which would read as
	// "quit without saving" and silently discard a resolution the user made.
	baseline := before.ModTime()
	if back := baseline.Add(-2 * time.Second); os.Chtimes(path, back, back) == nil {
		baseline = back
	}

	fields := strings.Fields(editor)
	c := exec.Command(fields[0], append(fields[1:], path)...) //nolint:gosec // the user's own $EDITOR, by definition
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if runErr := c.Run(); runErr != nil {
		return nil, fmt.Errorf("running %s: %w", fields[0], runErr)
	}

	after, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !after.ModTime().After(baseline) {
		return nil, errMergeAbandoned
	}
	return os.ReadFile(path) //nolint:gosec // path under weft's own config dir
}

func ensureTrailingNL(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
