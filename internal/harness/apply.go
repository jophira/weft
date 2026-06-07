package harness

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
}

// out returns the writer from ctx, defaulting to io.Discard when unset.
// cf. Java: Optional.orElse(OutputStream.nullOutputStream())
func applyOut(ctx ApplyCtx) io.Writer {
	if ctx.Out != nil {
		return ctx.Out
	}
	return io.Discard
}

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
//   - Owned by weft, content unchanged (skip): no write, log "· skip".
//   - Owned by weft, content changed (update): write, log "✓ wrote".
//   - Externally modified (conflict): back up, then write, log "! backed up".
//
// All conflicts are backed up before any writes occur. The manifest is updated
// with new hashes after a successful apply.
//
// filter, when non-nil, is called with each file's rel path (relative to stagedRoot)
// before processing; returning false skips the file entirely. Pass nil to accept all.
func applyWithManifest(stagedRoot, targetRoot, harnessName string, ctx ApplyCtx, renames map[string]string, filter func(rel string) bool) error {
	out := applyOut(ctx)

	m, err := manifest.Load(ctx.CfgDir, harnessName)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	var entries []fileEntry
	var conflicts []conflictFile
	newHashes := map[string]string{} // dst rel → staged sha256

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
		dst := rel
		if renamed, ok := renames[rel]; ok {
			dst = renamed
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
			fmt.Fprintf(out, "  · skip   %s\n", fe.dst)
			continue
		}
		fullDst := filepath.Join(targetRoot, fe.dst)
		if mkErr := os.MkdirAll(filepath.Dir(fullDst), 0o755); mkErr != nil {
			return fmt.Errorf("creating parent dir for %s: %w", fe.dst, mkErr)
		}
		if wErr := os.WriteFile(fullDst, fe.data, 0o644); wErr != nil { //nolint:gosec // path derived from harness config
			return fmt.Errorf("writing %s: %w", fe.dst, wErr)
		}
		fmt.Fprintf(out, "  ✓ wrote  %s\n", fe.dst)
	}

	m.Harness = harnessName
	m.Profile = ctx.ProfileName
	m.TargetRoot = targetRoot
	m.AppliedAt = time.Now()
	// Replace (not merge) so that deleted source files are pruned from the manifest.
	// maps.Copy would leave stale entries that cause false conflicts on subsequent applies.
	m.Files = newHashes
	// Rebuild SourceFiles from scratch for the same reason — only keep entries that
	// correspond to files present in this apply's staged tree.
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
				// content identical — skip write
				fmt.Fprintf(out, "  · skip   %s\n", rel)
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
	fmt.Fprintf(out, "  ✓ wrote  %s\n", rel)

	m.Harness = harnessName
	m.Profile = ctx.ProfileName
	m.TargetRoot = filepath.Dir(absPath)
	m.AppliedAt = time.Now()
	m.Files[rel] = contentHash
	return manifest.Save(ctx.CfgDir, m)
}

// applyToHomeDir resolves the home directory, ensures dotSubdir exists under it,
// then delegates to applyWithManifest. It is the common Apply body for harnesses
// whose target is a single directory under $HOME (e.g. ~/.claude, ~/.aider).
func applyToHomeDir(stagedRoot, dotSubdir, harnessName string, ctx ApplyCtx, renames map[string]string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}
	target := filepath.Join(home, dotSubdir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("ensuring ~/%s exists: %w", dotSubdir, err)
	}
	return applyWithManifest(stagedRoot, target, harnessName, ctx, renames, nil)
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
