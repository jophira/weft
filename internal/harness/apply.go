package harness

import (
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

	statusUnchanged = "unchanged"
	statusWrote     = "wrote"
	statusRemoved   = "removed"
	statusKept      = "kept"
	statusSkipped   = "skipped"
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
		// Read the staged file once; hash in-memory to avoid a second syscall later.
		stagedData, rdErr := os.ReadFile(path) //nolint:gosec // path comes from WalkDir over a weft-controlled staged dir, not user input
		if rdErr != nil {
			return fmt.Errorf("reading staged %s: %w", dst, rdErr)
		}
		stagedHash := manifest.HashBytes(stagedData)
		newHashes[dst] = stagedHash

		fe := fileEntry{srcPath: path, dst: dst, stagedHash: stagedHash}

		fullDst := filepath.Join(targetRoot, dst)
		existing, readErr := os.ReadFile(fullDst)
		switch {
		case os.IsNotExist(readErr):
			// new file — nothing on disk yet; retain stagedData for write
			fe.data = stagedData
		case readErr != nil:
			return fmt.Errorf("reading %s: %w", fullDst, readErr)
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
				conflicts = append(conflicts, conflictFile{rel: dst, abs: fullDst})
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
		fullDst := filepath.Join(targetRoot, fe.dst)
		if mkErr := os.MkdirAll(filepath.Dir(fullDst), 0o755); mkErr != nil {
			return fmt.Errorf("creating parent dir for %s: %w", fe.dst, mkErr)
		}
		if wErr := os.WriteFile(fullDst, fe.data, 0o644); wErr != nil { //nolint:gosec // path derived from harness config
			return fmt.Errorf("writing %s: %w", fe.dst, wErr)
		}
		fmt.Fprintf(out, logWrote, statusWrote, fe.dst)
	}

	// Remove files the previous apply staged but this one does not, so a profile
	// switch leaves no orphans behind. Prunes Files entries for whatever it deletes;
	// user-edited files it declines to delete keep their entry.
	if err := pruneDropped(prevStaged, newHashes, targetRoot, m, out); err != nil {
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

	existing, readErr := os.ReadFile(absPath)
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

	if err := os.WriteFile(absPath, content, 0o644); err != nil { //nolint:gosec // path is resolved from harness config, not user input
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
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
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
	targetRoot string,
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
		full := filepath.Join(targetRoot, rel)
		existing, readErr := os.ReadFile(full) //nolint:gosec // rel comes from the manifest weft itself wrote
		switch {
		case os.IsNotExist(readErr):
			delete(m.Files, rel) // already gone — just forget it

		case readErr != nil:
			return fmt.Errorf("reading dropped file %s: %w", rel, readErr)

		default:
			if knownHash, owned := m.Files[rel]; !owned || manifest.HashBytes(existing) != knownHash {
				// Edited since weft wrote it (or never weft's) — not ours to delete.
				fmt.Fprintf(out, logKept, statusKept, rel)
				continue
			}
			if rmErr := os.Remove(full); rmErr != nil {
				return fmt.Errorf("removing dropped file %s: %w", rel, rmErr)
			}
			delete(m.Files, rel)
			fmt.Fprintf(out, logRemoved, statusRemoved, rel)
			pruneEmptyDirs(filepath.Dir(full), targetRoot)
		}
	}
	return nil
}

// pruneEmptyDirs walks up from dir removing empty directories, stopping at (and
// never removing) root. Non-empty directories abort the walk, as os.Remove fails
// on them — the error is the signal to stop, not a fault.
func pruneEmptyDirs(dir, root string) {
	for dir != root && strings.HasPrefix(dir, root) {
		if err := os.Remove(dir); err != nil {
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

	for _, c := range conflicts {
		dst := filepath.Join(backupDir, c.rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", fmt.Errorf("creating backup dir for %s: %w", c.rel, err)
		}
		data, err := os.ReadFile(c.abs)
		if err != nil {
			return "", fmt.Errorf("reading %s for backup: %w", c.rel, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil { //nolint:gosec // dst is derived from config backup dir, not user input
			return "", fmt.Errorf("backing up %s: %w", c.rel, err)
		}
	}
	return backupDir, nil
}
