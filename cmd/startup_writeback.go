package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/watch"
)

// startupWriteBack detects files in the target harness that have been modified
// externally since the last apply, and writes them back to their source before
// the next apply runs. This preserves edits to target files (e.g. ~/.claude/CLAUDE.md)
// rather than silently backing them up and overwriting them.
//
// For each staged file:
//   - If the on-disk target hash differs from the manifest hash: externally modified.
//   - Single-source files: written back via writeBackSingleSource.
//   - Multi-source files: written back via writeBackMergedSource.
//   - Unresolvable files (no owning source, no write_back.default): backed up with a warning.
//
// After write-back the source files contain the target edits, so the subsequent
// h.Apply() call will re-merge them and produce a file identical to what is on
// disk — no backup, no overwrite.
func startupWriteBack(
	stagedDir string,
	target string,
	cfgDir string,
	p *profile.Profile,
	srcs []source.Source,
) error {
	targetRoot := harnessTargetRoot(cfgDir, target)
	if targetRoot == "" {
		// No prior apply recorded — nothing to write back.
		return nil
	}

	m, err := manifest.Load(cfgDir, target)
	if err != nil {
		return fmt.Errorf("loading manifest for startup write-back: %w", err)
	}

	// Build srcMap once before the walk — reused for every file in the tree.
	// cf. Java: compute a HashMap<String,Source> before the Files.walk() stream.
	wbSrcMap := buildSrcMap(srcs)

	// Tracks whether any write-back refreshed a manifest hash during the walk, so
	// the manifest is saved once at the end rather than per file.
	dirty := false

	walkErr := filepath.WalkDir(stagedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(stagedDir, path)
		if relErr != nil {
			return relErr
		}

		// Check whether the on-disk target file is externally modified.
		fullTarget := filepath.Join(targetRoot, rel)
		existing, readErr := os.ReadFile(fullTarget)
		if os.IsNotExist(readErr) {
			return nil // file not on disk yet — nothing to write back
		}
		if readErr != nil {
			return fmt.Errorf("reading target %s: %w", rel, readErr)
		}

		existingHash := manifest.HashBytes(existing)
		knownHash, owned := m.Files[rel]
		if !owned || existingHash == knownHash {
			// Either weft doesn't own it or it hasn't changed since the last apply.
			return nil
		}

		// File is externally modified — attempt write-back.
		c := watch.TargetChange{Root: targetRoot, Rel: rel}

		performed, wbErr := dispatchWriteBack(m, c, p, wbSrcMap)
		if wbErr != nil {
			return fmt.Errorf("write-back for %s: %w", rel, wbErr)
		}

		if performed {
			dirty = true
			// Identify the source name for the output message.
			srcName := resolvedSourceName(rel, p, srcs, m)
			fmt.Printf("[weft] startup write-back: %s → %s\n", rel, srcName)
			return nil
		}

		// Unresolvable: back up the target file and warn the user.
		backupPath, backupErr := backupStartupFile(rel, fullTarget, target, cfgDir)
		if backupErr != nil {
			return fmt.Errorf("backing up unresolvable file %s: %w", rel, backupErr)
		}
		fmt.Printf("[weft] warning: %s could not be written back — no source found. Backed up to %s\n",
			rel, locate.Tilde(backupPath))
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	// Persist the refreshed hashes before the caller applies, so apply sees the
	// written-back files as reconciled rather than externally modified.
	if dirty {
		if err := manifest.Save(cfgDir, m); err != nil {
			return fmt.Errorf("saving manifest after startup write-back: %w", err)
		}
	}
	return nil
}

// resolvedSourceName returns a human-readable source name for the write-back log.
// For single-source files it uses owningSource; for multi-source it returns the
// source names joined with "+".
func resolvedSourceName(rel string, p *profile.Profile, srcs []source.Source, m *manifest.Manifest) string {
	if names := m.SourceFiles[rel]; len(names) > 1 {
		return strings.Join(names, "+")
	}
	name, _, ok := owningSource(rel, p, srcs)
	if ok {
		return name
	}
	return "unknown"
}

// backupStartupFile copies the target file to
// cfgDir/backups/<harness>/<timestamp>/<rel> and returns the backup path.
func backupStartupFile(rel, absTarget, harnessName, cfgDir string) (string, error) {
	ts := time.Now().Format("20060102-150405")
	dst := filepath.Join(cfgDir, "backups", harnessName, ts, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("creating backup dir: %w", err)
	}
	data, err := os.ReadFile(absTarget)
	if err != nil {
		return "", fmt.Errorf("reading %s for backup: %w", rel, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil { //nolint:gosec // dst is derived from config backup dir
		return "", fmt.Errorf("writing backup for %s: %w", rel, err)
	}
	return dst, nil
}
