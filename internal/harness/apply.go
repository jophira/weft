package harness

import (
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
)

// ApplyCtx carries per-apply metadata needed for manifest tracking and backups.
type ApplyCtx struct {
	ProfileName       string
	CfgDir            string
	SourceAttribution map[string][]string // rel path -> ordered source names (merged files only)
	Out               io.Writer           // destination for per-file apply logs; nil → io.Discard
	// AllowedClasses restricts projection to these classes (profile harness_sync).
	// A nil map means unrestricted: project every class the harness supports. An
	// empty non-nil map means project nothing, so "unset" and "explicitly empty"
	// stay distinguishable.
	AllowedClasses map[Class]bool
	// Held lists target-relative paths a detected conflict has frozen. Apply
	// leaves each of them exactly as it found it: no write, no backup, no manifest
	// change. See conflict.go — overwriting either side is the data loss the
	// detection exists to prevent, and backing one up first still moves the user's
	// file somewhere they did not put it.
	Held map[string]bool
}

// classAllowed reports whether the profile's harness_sync config permits this
// class. Unrestricted (nil) is the default so existing profiles are unaffected.
func (ctx ApplyCtx) classAllowed(c Class) bool {
	if ctx.AllowedClasses == nil {
		return true
	}
	return ctx.AllowedClasses[c]
}

// out returns the writer from ctx, defaulting to io.Discard when unset.
// cf. Java: Optional.orElse(OutputStream.nullOutputStream())
func applyOut(ctx ApplyCtx) io.Writer {
	if ctx.Out != nil {
		return ctx.Out
	}
	return io.Discard
}

// Per-file apply log lines. Statuses are padded to a common width so the file
// paths line up in a column regardless of which status is printed.
// cf. Java: String.format("%-9s", status) — Go uses the same %-Ns verb.
const (
	logUnchanged = "  · %-9s %s\n"
	logWrote     = "  ✓ %-9s %s\n"
	logRemoved   = "  − %-9s %s\n"
	logKept      = "  ! %-9s %s (edited since weft wrote it — no longer managed)\n"
	logHeld      = "  ! %-9s %s (conflict — both copies left as they are)\n"

	statusUnchanged = "unchanged"
	statusWrote     = "wrote"
	statusRemoved   = "removed"
	statusKept      = "kept"
	statusSkipped   = "skipped"
	statusHeld      = "held"
)

type conflictFile struct {
	rel string // path relative to targetRoot
	abs string // absolute path on disk
}

// fileEntry records what needs to happen for one staged file.
type fileEntry struct {
	srcPath    string // absolute path in staged dir
	dst        string // rel path in target (post-rename)
	stagedHash string
	data       []byte // staged file bytes; nil for skip=true entries (no write needed)
	skip       bool   // content identical — no write needed
	conflict   bool   // externally modified — back up before writing
}

// applyWithManifest is the manifest-aware apply for all harnesses that copy a
// directory tree to a target root.
//
// For each staged file:
//   - Not on disk yet (new): write, log "✓ wrote".
//   - Owned by weft, content identical (skip): no write, log "· unchanged".
//   - Owned by weft, content changed (update): write, log "✓ wrote".
//   - Externally modified (conflict): back up, then write, log "! backed up".
//
// Files the previous apply staged but this one does not (e.g. after a profile
// switch) are dropped: removed from the target when weft still owns them, or left
// in place with a warning when the user has edited them since. See pruneDropped.
//
// All conflicts are backed up before any writes occur. The manifest is updated
// with new hashes after a successful apply.
//
// filter, when non-nil, is called with each file's rel path (relative to stagedRoot)
// before processing; returning false skips the file entirely. Pass nil to accept all.
//
// h, when non-nil, supplies per-class placement. Files whose class the harness has
// no native home for are not written at all — see routeStaged. A nil h keeps the
// pre-class-model behaviour of copying every file at its staged path.
func applyWithManifest(stagedRoot, targetRoot, harnessName string, ctx ApplyCtx, renames map[string]string, filter func(rel string) bool, h Harness) error {
	out := applyOut(ctx)

	// Every read, write, mkdir and delete below goes through this root rather
	// than through a joined absolute path. os.Root refuses any name that leaves
	// the directory, including one that leaves through a symlink, so a link
	// planted inside a harness tree can no longer redirect a projection, a
	// backup or a prune somewhere else (#278).
	root, err := os.OpenRoot(targetRoot)
	if err != nil {
		return fmt.Errorf("opening target root %s: %w", targetRoot, err)
	}
	defer root.Close() //nolint:errcheck // read-mostly fd; close failure cannot affect what was already written

	m, err := manifest.Load(ctx.CfgDir, harnessName)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}
	// Capture what the previous apply projected before any of it is overwritten
	// below — the difference against this apply's staged set is what got dropped.
	prevStaged := m.StagedSet()

	var entries []fileEntry
	var conflicts []conflictFile
	newHashes := map[string]string{} // dst rel → staged sha256
	skipped := map[Class]int{}       // class → files not written (no native home)
	excluded := map[Class]int{}      // class → files withheld by harness_sync config
	var held []string                // dst paths frozen by an unresolved conflict

	err = filepath.WalkDir(stagedRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(stagedRoot, path)
		if relErr != nil {
			return relErr
		}
		// filter lets harnesses restrict which files are processed (e.g. by extension).
		// cf. Java: Predicate<String> — Go uses plain function values instead.
		if filter != nil && !filter(rel) {
			return nil
		}
		cls := stagedClass(rel)
		if !ctx.classAllowed(cls) {
			excluded[cls]++
			return nil
		}
		dst, ok := routeStaged(rel, renames, h)
		if !ok {
			skipped[cls]++
			return nil
		}
		if ctx.Held[dst] {
			// Keep the recorded hash rather than the one on disk: the manifest is
			// weft's record of what it last wrote, and adopting the user's bytes here
			// would silently claim their edit and make the conflict vanish. Keeping
			// the path in newHashes keeps it in the staged set, so pruneDropped does
			// not treat a held file as one the profile dropped.
			if known, owned := m.Files[dst]; owned {
				newHashes[dst] = known
			}
			held = append(held, dst)
			return nil
		}
		// Read the staged file once; hash in-memory to avoid a second syscall later.
		stagedData, rdErr := os.ReadFile(path) //nolint:gosec // path comes from WalkDir over a weft-controlled staged dir, not user input
		if rdErr != nil {
			return fmt.Errorf("reading staged %s: %w", dst, rdErr)
		}
		stagedHash := manifest.HashBytes(stagedData)
		newHashes[dst] = stagedHash

		fe := fileEntry{srcPath: path, dst: dst, stagedHash: stagedHash}

		existing, readErr := root.ReadFile(dst)
		switch {
		case os.IsNotExist(readErr):
			// new file — nothing on disk yet; retain stagedData for write
			fe.data = stagedData
		case readErr != nil:
			// Unlike the prune path, this one cannot resolve to "leave it alone":
			// carrying on would report a successful apply while the user's rules
			// never reached the file. Fail loudly and name the remedy.
			return fmt.Errorf(
				"reading %s under %s: %w\n"+
					"weft will not write through a path that leaves the harness directory. "+
					"If a symlink there is deliberate, point the harness at the real directory instead",
				dst, targetRoot, readErr)
		default:
			existingHash := manifest.HashBytes(existing)
			if knownHash, owned := m.Files[dst]; owned && existingHash == knownHash {
				// weft owns this file and nothing changed on disk externally
				if stagedHash == knownHash {
					fe.skip = true // staged content identical to what we last wrote; no data needed
				} else {
					fe.data = stagedData // weft-owned update — write new content
				}
			} else {
				// not owned or externally modified
				fe.conflict = true
				fe.data = stagedData
				conflicts = append(conflicts, conflictFile{rel: dst, abs: filepath.Join(targetRoot, dst)})
			}
		}
		entries = append(entries, fe)
		return nil
	})
	if err != nil {
		return err
	}

	reportSkipped(out, skipped, h)
	reportExcluded(out, excluded)
	slices.Sort(held)
	for _, dst := range held {
		fmt.Fprintf(out, logHeld, statusHeld, dst)
	}

	// Back up all conflicts before any write so the user never sees partial state.
	if len(conflicts) > 0 {
		backupDir, bErr := backupConflicts(conflicts, harnessName, ctx.CfgDir)
		if bErr != nil {
			return bErr
		}
		fmt.Fprintf(out, "  ! %d file(s) externally modified — backed up to %s\n",
			len(conflicts), locate.Tilde(backupDir))
		for _, c := range conflicts {
			fmt.Fprintf(out, "      %s\n", c.rel)
		}
	}

	// Write each file; skip unchanged ones.
	for _, fe := range entries {
		if fe.skip {
			fmt.Fprintf(out, logUnchanged, statusUnchanged, fe.dst)
			continue
		}
		if mkErr := root.MkdirAll(filepath.Dir(fe.dst), 0o755); mkErr != nil {
			return fmt.Errorf("creating parent dir for %s: %w", fe.dst, mkErr)
		}
		if wErr := root.WriteFile(fe.dst, fe.data, 0o644); wErr != nil {
			return fmt.Errorf("writing %s: %w", fe.dst, wErr)
		}
		fmt.Fprintf(out, logWrote, statusWrote, fe.dst)
	}

	// Remove files the previous apply staged but this one does not, so a profile
	// switch leaves no orphans behind. Prunes Files entries for whatever it deletes;
	// user-edited files it declines to delete keep their entry.
	if err := pruneDropped(prevStaged, newHashes, root, m, out); err != nil {
		return err
	}

	m.Harness = harnessName
	m.Profile = ctx.ProfileName
	m.TargetRoot = targetRoot
	m.AppliedAt = time.Now()
	// Merge, don't replace. Files is the durable ownership record: dropping an entry
	// because the active profile no longer stages it makes weft forget it wrote the
	// file, so the next apply that stages it again mistakes its own output for a
	// user edit (issue #209). pruneDropped has already removed the entries whose
	// files are genuinely gone from disk.
	maps.Copy(m.Files, newHashes)
	m.Staged = slices.Sorted(maps.Keys(newHashes))
	// Rebuild SourceFiles from scratch — only keep entries that correspond to files
	// present in this apply's staged tree.
	newSourceFiles := make(map[string][]string)
	for rel, sources := range ctx.SourceAttribution {
		if _, ok := newHashes[rel]; ok {
			newSourceFiles[rel] = sources
		}
	}
	if len(newSourceFiles) > 0 {
		m.SourceFiles = newSourceFiles
	} else {
		m.SourceFiles = nil
	}
	return manifest.Save(ctx.CfgDir, m)
}

// trackAndWriteFile handles manifest check/backup/write for harnesses that write
// a single computed file (e.g. Cursor prepends frontmatter before writing).
// content is the final bytes written to absPath; rel is its path relative to the parent dir.
func trackAndWriteFile(absPath, rel, harnessName string, content []byte, ctx ApplyCtx) error {
	out := applyOut(ctx)

	m, err := manifest.Load(ctx.CfgDir, harnessName)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	contentHash := manifest.HashBytes(content)

	// The parent is the harness's own directory; rel names the one file inside
	// it. Going through a root means a symlink at that name cannot redirect the
	// write out of the harness (#278).
	root, err := os.OpenRoot(filepath.Dir(absPath))
	if err != nil {
		return fmt.Errorf("opening %s: %w", filepath.Dir(absPath), err)
	}
	defer root.Close() //nolint:errcheck // close failure cannot unwrite the file

	existing, readErr := root.ReadFile(rel)
	switch {
	case os.IsNotExist(readErr):
		// new file — fall through to write

	case readErr != nil:
		return fmt.Errorf("reading %s: %w", absPath, readErr)

	default:
		existingHash := manifest.HashBytes(existing)
		if knownHash, owned := m.Files[rel]; owned && existingHash == knownHash {
			if contentHash == knownHash {
				// content identical — no write needed
				fmt.Fprintf(out, logUnchanged, statusUnchanged, rel)
				return nil
			}
			// weft-owned update — fall through to write
		} else {
			// externally modified — back up first
			backupDir, bErr := backupConflicts([]conflictFile{{rel: rel, abs: absPath}}, harnessName, ctx.CfgDir)
			if bErr != nil {
				return bErr
			}
			fmt.Fprintf(out, "  ! 1 file(s) externally modified — backed up to %s\n", locate.Tilde(backupDir))
			fmt.Fprintf(out, "      %s\n", rel)
		}
	}

	if err := root.WriteFile(rel, content, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", absPath, err)
	}
	fmt.Fprintf(out, logWrote, statusWrote, rel)

	m.Harness = harnessName
	m.Profile = ctx.ProfileName
	m.TargetRoot = filepath.Dir(absPath)
	m.AppliedAt = time.Now()
	m.Files[rel] = contentHash
	// This harness projects exactly one file, so it is the whole staged set. Kept in
	// sync with Files so pruneDropped never sees it as dropped (issue #209).
	m.Staged = []string{rel}
	return manifest.Save(ctx.CfgDir, m)
}

// applyToHomeDir resolves the home directory, ensures dotSubdir exists under it,
// then delegates to applyWithManifest. It is the common Apply body for harnesses
// whose target is a single directory under $HOME (e.g. ~/.claude, ~/.aider).
func applyToHomeDir(stagedRoot, dotSubdir string, h Harness, ctx ApplyCtx, renames map[string]string) error {
	home := locate.HarnessHome()
	if home == "" {
		return fmt.Errorf("resolving home directory")
	}
	target := filepath.Join(home, dotSubdir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("ensuring ~/%s exists: %w", dotSubdir, err)
	}
	return applyWithManifest(stagedRoot, target, h.Name(), ctx, renames, nil, h)
}

// pruneDropped removes target files that the previous apply staged but this one
// does not — the residue of a profile switch or a deleted source file.
//
// For each dropped path, on-disk content decides what happens:
//   - Matches the manifest hash: weft wrote it and nobody has touched it since, so
//     it is deleted and its manifest entry pruned. Logged "− removed".
//   - Differs: the user has edited it. Deleting it would destroy work weft has no
//     claim over, so it is left in place and logged "! kept". Its manifest entry
//     survives, which keeps write-back working if the file is ever staged again.
//   - Already gone: nothing to do beyond pruning the manifest entry.
//
// Empty parent directories left behind are removed, so dropping a whole skill does
// not leave a bare directory in ~/.claude/skills.
func pruneDropped(
	prevStaged map[string]struct{},
	nowStaged map[string]string,
	root *os.Root,
	m *manifest.Manifest,
	out io.Writer,
) error {
	// Sort so the log (and the tests reading it) have a stable order.
	dropped := make([]string, 0)
	for rel := range prevStaged {
		// Sidecar keys name a file outside targetRoot and cannot be resolved by
		// joining. StagedSet already filters them, but pruning by path is the one
		// place where letting one through corrupts state rather than erroring
		// cleanly, so refuse them here too.
		if manifest.IsSidecarKey(rel) {
			continue
		}
		if _, stillStaged := nowStaged[rel]; !stillStaged {
			dropped = append(dropped, rel)
		}
	}
	slices.Sort(dropped)

	for _, rel := range dropped {
		existing, readErr := root.ReadFile(rel)
		switch {
		case os.IsNotExist(readErr):
			delete(m.Files, rel) // already gone — just forget it

		case readErr != nil:
			// Anything else — a symlink escaping the root, a permission error, a
			// broken link — means weft cannot establish that this file is the one
			// it wrote. This is a delete path, so an uncertain answer resolves to
			// keeping the file and its manifest entry, not to removing it and not
			// to failing the whole apply over one dropped path (#278).
			fmt.Fprintf(out, logKept, statusKept, rel)

		default:
			if knownHash, owned := m.Files[rel]; !owned || manifest.HashBytes(existing) != knownHash {
				// Edited since weft wrote it (or never weft's) — not ours to delete.
				fmt.Fprintf(out, logKept, statusKept, rel)
				continue
			}
			if rmErr := root.Remove(rel); rmErr != nil {
				return fmt.Errorf("removing dropped file %s: %w", rel, rmErr)
			}
			delete(m.Files, rel)
			fmt.Fprintf(out, logRemoved, statusRemoved, rel)
			pruneEmptyDirs(root, filepath.Dir(rel))
		}
	}
	return nil
}

// pruneEmptyDirs walks up from dir removing empty directories, stopping at (and
// never removing) the root itself. Non-empty directories abort the walk, as
// Remove fails on them — the error is the signal to stop, not a fault.
//
// dir is relative to root, so "." is the root and the terminating case. The
// containment that the old prefix comparison was reaching for is now the root's
// own: it cannot delete anything outside, symlinked or otherwise.
func pruneEmptyDirs(root *os.Root, dir string) {
	for dir != "." && dir != string(filepath.Separator) {
		if err := root.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// backupConflicts copies each conflict file into cfgDir/backups/<harness>/<timestamp>/,
// preserving relative path structure. Returns the backup directory path.
func backupConflicts(conflicts []conflictFile, harnessName, cfgDir string) (string, error) {
	ts := time.Now().Format("20060102-150405")
	backupDir := filepath.Join(cfgDir, "backups", harnessName, ts)

	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("creating backup dir: %w", err)
	}
	// rel is a harness-relative path that reached weft from the projection, so
	// the backup copy is written through a root too: a backup must not be the
	// thing that writes outside the config dir (#278).
	root, err := os.OpenRoot(backupDir)
	if err != nil {
		return "", fmt.Errorf("opening backup dir: %w", err)
	}
	defer root.Close() //nolint:errcheck // failure to close cannot unwrite the backups

	for _, c := range conflicts {
		if err := root.MkdirAll(filepath.Dir(c.rel), 0o755); err != nil {
			return "", fmt.Errorf("creating backup dir for %s: %w", c.rel, err)
		}
		data, err := os.ReadFile(c.abs)
		if err != nil {
			return "", fmt.Errorf("reading %s for backup: %w", c.rel, err)
		}
		if err := root.WriteFile(c.rel, data, 0o644); err != nil {
			return "", fmt.Errorf("backing up %s: %w", c.rel, err)
		}
	}
	return backupDir, nil
}
