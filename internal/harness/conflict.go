package harness

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jophira/weft/internal/manifest"
)

// Conflict detection is the guard on fan-in.
//
// Write-back takes an edit made inside one harness, pushes it to the owning
// source, and the next apply fans it out to every other harness. That is safe
// while only one copy has moved. When two harnesses have both been edited since
// the last apply, whichever one write-back happens to visit last wins and the
// other edit is gone — not recoverable from anywhere, because the source now
// simply holds the winner's bytes.
//
// So weft refuses. Both copies stay on disk exactly as the user left them, the
// apply holds the file instead of overwriting either side, and the user names a
// winner with `weft resolve` (ADR 0004 D5).

// ConflictTarget is one harness's state as conflict detection sees it: where its
// files live, which adapter routes them, and what weft last recorded writing.
type ConflictTarget struct {
	Harness  string
	Root     string // absolute path of the harness config root
	H        Harness
	Manifest *manifest.Manifest
}

// Divergence is one harness's copy of a canonical file that no longer matches
// the hash the manifest recorded at the last apply.
type Divergence struct {
	Harness   string
	Rel       string    // path relative to that harness's root, post-routing
	Abs       string    // absolute path on disk
	AppliedAt time.Time // when weft last wrote this harness
}

// Conflict is two or more harnesses diverging on the same canonical file.
type Conflict struct {
	// Canonical is the staged-relative path, slash-separated. It is the name the
	// user passes to `weft resolve`, because it is the one name that means the
	// same thing in every harness.
	Canonical string
	Diverged  []Divergence // at least two, ordered by harness name
	Since     time.Time    // earliest apply among the diverged harnesses
}

// DetectConflicts reports every staged file that two or more targets have
// diverged on since their last apply.
//
// Detection is manifest-driven rather than filesystem-driven: a file weft wrote
// and a file the user wrote are byte-identical in kind, so the recorded hash is
// the only evidence of what changed after weft last touched it.
func DetectConflicts(stagedRoot string, targets []ConflictTarget) ([]Conflict, error) {
	var out []Conflict
	err := filepath.WalkDir(stagedRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		rel, relErr := filepath.Rel(stagedRoot, path)
		if relErr != nil {
			return relErr
		}
		c, found, cErr := conflictFor(rel, targets)
		if cErr != nil {
			return cErr
		}
		if found {
			out = append(out, c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Canonical < out[j].Canonical })
	return out, nil
}

// conflictFor resolves one staged path against every target.
func conflictFor(rel string, targets []ConflictTarget) (Conflict, bool, error) {
	switch stagedClass(rel) {
	case ClassInstructions, ClassMCP:
		// Neither travels as a copied file. The instruction file is a managed block
		// inside a document whose surrounding prose belongs to the user, and MCP
		// config is merged key-by-key into a file holding unrelated tool state. Both
		// reconcile through their own paths, so hashing them whole here would report
		// a conflict on every apply for edits weft never claimed.
		return Conflict{}, false, nil
	case ClassCommands, ClassAgents, ClassSkills, ClassOther:
	}

	var diverged []Divergence
	for _, t := range targets {
		if t.Manifest == nil || t.Root == "" {
			continue
		}
		// nil renames is correct here rather than a shortcut: the only rename any
		// harness declares is the instruction file's, and that class returned above.
		dst, ok := routeStaged(rel, nil, t.H)
		if !ok {
			continue // class has no native home in this harness — nothing to diverge
		}
		known, owned := t.Manifest.Files[dst]
		if !owned {
			continue // weft never wrote it here, so it has no recorded hash to differ from
		}
		abs := filepath.Join(t.Root, dst)
		data, readErr := os.ReadFile(abs) //nolint:gosec // dst is routed from weft's own staged tree
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return Conflict{}, false, fmt.Errorf("reading %s: %w", abs, readErr)
		}
		if manifest.HashBytes(data) == known {
			continue
		}
		diverged = append(diverged, Divergence{
			Harness: t.Harness, Rel: dst, Abs: abs, AppliedAt: t.Manifest.AppliedAt,
		})
	}
	// One diverged copy is the ordinary write-back case, not a conflict: there is
	// exactly one edit, so there is nothing to choose between.
	if len(diverged) < 2 {
		return Conflict{}, false, nil
	}
	sort.Slice(diverged, func(i, j int) bool { return diverged[i].Harness < diverged[j].Harness })
	return Conflict{
		Canonical: filepath.ToSlash(rel),
		Diverged:  diverged,
		Since:     earliestApply(diverged),
	}, true, nil
}

// earliestApply picks the oldest recorded apply among the diverged harnesses.
//
// Each copy changed after its own harness's apply, so the earliest is the one
// timestamp the report can name that is true of all of them. Quoting the latest
// instead would claim an edit happened after a moment weft cannot show it did.
func earliestApply(diverged []Divergence) time.Time {
	var earliest time.Time
	for _, d := range diverged {
		if d.AppliedAt.IsZero() {
			continue
		}
		if earliest.IsZero() || d.AppliedAt.Before(earliest) {
			earliest = d.AppliedAt
		}
	}
	return earliest
}

// Harnesses lists the diverged harness names in report order.
func (c Conflict) Harnesses() []string {
	names := make([]string, len(c.Diverged))
	for i, d := range c.Diverged {
		names[i] = d.Harness
	}
	return names
}

// Report renders the user-facing conflict message. now decides how the
// timestamp is rendered, so callers in tests can pin it.
func (c Conflict) Report(now time.Time) string {
	return fmt.Sprintf("! conflict: %s changed in %s since %s\n  → weft resolve %s --take %s\n",
		c.Canonical,
		joinAnd(c.Harnesses()),
		sinceLabel(c.Since, now),
		c.Canonical,
		strings.Join(c.Harnesses(), "|"))
}

// FormatConflicts writes every conflict's report to w.
func FormatConflicts(w io.Writer, conflicts []Conflict, now time.Time) {
	for _, c := range conflicts {
		fmt.Fprint(w, c.Report(now))
	}
}

// sinceLabel renders an apply time as a bare clock reading when it happened
// today and a full date otherwise. "since 14:02" is unambiguous for an apply an
// hour ago and actively misleading for one last week.
func sinceLabel(t, now time.Time) string {
	if t.IsZero() {
		return "the last apply"
	}
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("2006-01-02 15:04")
}

// joinAnd renders names as prose: "a and b", "a, b and c".
func joinAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// HeldPaths maps each harness to the target-relative paths a conflict freezes.
// Apply consults it to leave both sides untouched rather than backing one up and
// overwriting it — a backup the user has to go and find is still a surprise.
func HeldPaths(conflicts []Conflict) map[string]map[string]bool {
	held := map[string]map[string]bool{}
	for _, c := range conflicts {
		for _, d := range c.Diverged {
			if held[d.Harness] == nil {
				held[d.Harness] = map[string]bool{}
			}
			held[d.Harness][d.Rel] = true
		}
	}
	return held
}

// ErrTakeMerge is returned when the user asks for a three-way merge. ADR 0004
// specifies no merge algorithm, and inventing one here would mean guessing at
// how two prose files combine — the guess would be wrong silently, which is the
// failure conflict detection exists to prevent.
var ErrTakeMerge = fmt.Errorf(
	"--take merge is not implemented: weft has no merge algorithm for harness files, " +
		"so it would have to guess; pick a side, or merge the two files by hand and re-apply")

// ResolveRequest names the winning side of one conflict.
type ResolveRequest struct {
	Conflict Conflict
	Take     string // harness whose copy wins
	// SourcePath is the source file that receives the winner's bytes. Without it
	// the next apply would re-stage the pre-conflict content and undo the
	// resolution. Empty when the caller could not resolve an owning source, in
	// which case only the harness copies are reconciled.
	SourcePath string
	CfgDir     string
}

// ResolveResult records what Resolve did, for the caller to report.
type ResolveResult struct {
	Winner     string
	Rewritten  []Divergence // the losing copies, now holding the winner's bytes
	BackupDir  string       // where the losing copies were preserved first
	SourcePath string       // source file updated, or "" when none was resolved
}

// Resolve settles a conflict by taking one harness's copy.
//
// The losing copies are never deleted and never left stale: each is backed up
// as it stood, then rewritten from the winner so every harness agrees again. The
// backup is not optional — the user chose a winner, not the destruction of the
// other side's text, and a resolution they regret an hour later has to be
// recoverable.
func Resolve(req ResolveRequest) (ResolveResult, error) {
	if strings.EqualFold(strings.TrimSpace(req.Take), "merge") {
		return ResolveResult{}, ErrTakeMerge
	}
	winner, ok := pickWinner(req.Conflict, req.Take)
	if !ok {
		return ResolveResult{}, fmt.Errorf(
			"%q is not one of the harnesses that changed %s — pick one of %s",
			req.Take, req.Conflict.Canonical, strings.Join(req.Conflict.Harnesses(), ", "))
	}

	content, err := os.ReadFile(winner.Abs) //nolint:gosec // Abs was resolved from a harness root during detection
	if err != nil {
		return ResolveResult{}, fmt.Errorf("reading the %s copy of %s: %w", winner.Harness, req.Conflict.Canonical, err)
	}

	losers := make([]Divergence, 0, len(req.Conflict.Diverged)-1)
	backups := make([]conflictFile, 0, len(req.Conflict.Diverged)-1)
	for _, d := range req.Conflict.Diverged {
		if d.Harness == winner.Harness {
			continue
		}
		losers = append(losers, d)
		backups = append(backups, conflictFile{rel: filepath.Join(d.Harness, d.Rel), abs: d.Abs})
	}

	backupDir, err := backupConflicts(backups, resolveBackupName, req.CfgDir)
	if err != nil {
		return ResolveResult{}, err
	}

	for _, d := range losers {
		if mkErr := os.MkdirAll(filepath.Dir(d.Abs), 0o755); mkErr != nil {
			return ResolveResult{}, fmt.Errorf("creating parent dir for %s: %w", d.Abs, mkErr)
		}
		if wErr := os.WriteFile(d.Abs, content, 0o644); wErr != nil { //nolint:gosec // Abs is under a harness root weft already manages
			return ResolveResult{}, fmt.Errorf("writing the resolved %s to %s: %w", req.Conflict.Canonical, d.Harness, wErr)
		}
	}

	// Every copy now holds the same bytes, so every manifest must record them.
	// Leaving a stale hash behind would make the next apply re-detect the conflict
	// weft has just settled.
	hash := manifest.HashBytes(content)
	for _, d := range req.Conflict.Diverged {
		m, mErr := manifest.Load(req.CfgDir, d.Harness)
		if mErr != nil {
			return ResolveResult{}, fmt.Errorf("loading manifest for %s: %w", d.Harness, mErr)
		}
		m.Files[d.Rel] = hash
		if sErr := manifest.Save(req.CfgDir, m); sErr != nil {
			return ResolveResult{}, fmt.Errorf("recording the resolved hash for %s: %w", d.Harness, sErr)
		}
	}

	if req.SourcePath != "" {
		if mkErr := os.MkdirAll(filepath.Dir(req.SourcePath), 0o755); mkErr != nil {
			return ResolveResult{}, fmt.Errorf("creating source dir for %s: %w", req.Conflict.Canonical, mkErr)
		}
		if wErr := os.WriteFile(req.SourcePath, content, 0o644); wErr != nil { //nolint:gosec // SourcePath comes from the registered source root
			return ResolveResult{}, fmt.Errorf("writing %s back to its source: %w", req.Conflict.Canonical, wErr)
		}
	}

	return ResolveResult{
		Winner:     winner.Harness,
		Rewritten:  losers,
		BackupDir:  backupDir,
		SourcePath: req.SourcePath,
	}, nil
}

// resolveBackupName is the harness slot conflict backups are filed under. It is
// not a harness, deliberately: the backup holds copies from several of them, so
// filing it under any one would misattribute the rest.
const resolveBackupName = "resolve"

func pickWinner(c Conflict, take string) (Divergence, bool) {
	for _, d := range c.Diverged {
		if strings.EqualFold(d.Harness, take) {
			return d, true
		}
	}
	return Divergence{}, false
}
