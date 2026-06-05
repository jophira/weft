package harness

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
)

// ApplyCtx carries per-apply metadata needed for manifest tracking and backups.
type ApplyCtx struct {
	ProfileName string
	CfgDir      string
}

type conflictFile struct {
	rel string // path relative to targetRoot
	abs string // absolute path on disk
}

// applyWithManifest is the manifest-aware replacement for copyWithRename.
//
// For each file that would be written to targetRoot:
//   - File does not exist: write silently.
//   - File exists, hash matches manifest (weft owns it): overwrite silently.
//   - File exists, hash differs (externally modified): back up then overwrite.
//
// All conflicts are backed up together in one timestamped directory before any
// write occurs. The manifest is updated with new hashes after a successful apply.
func applyWithManifest(stagedRoot, targetRoot, harnessName string, ctx ApplyCtx, renames map[string]string) error {
	m, err := manifest.Load(ctx.CfgDir, harnessName)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	var conflicts []conflictFile
	newFiles := map[string]string{} // dest rel path → sha256 of staged content

	err = filepath.WalkDir(stagedRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(stagedRoot, path)
		if err != nil {
			return err
		}
		dst := rel
		if renamed, ok := renames[rel]; ok {
			dst = renamed
		}
		stagedHash, err := manifest.HashFile(path)
		if err != nil {
			return err
		}
		newFiles[dst] = stagedHash

		fullDst := filepath.Join(targetRoot, dst)
		existing, readErr := os.ReadFile(fullDst)
		if os.IsNotExist(readErr) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", fullDst, readErr)
		}
		if knownHash, owned := m.Files[dst]; owned && manifest.HashBytes(existing) == knownHash {
			return nil // weft owns it and it hasn't been touched externally
		}
		conflicts = append(conflicts, conflictFile{rel: dst, abs: fullDst})
		return nil
	})
	if err != nil {
		return err
	}

	if len(conflicts) > 0 {
		backupDir, err := backupConflicts(conflicts, harnessName, ctx.CfgDir)
		if err != nil {
			return err
		}
		fmt.Printf("  ! %d file(s) externally modified — backed up to %s\n",
			len(conflicts), locate.Tilde(backupDir))
		for _, c := range conflicts {
			fmt.Printf("      %s\n", c.rel)
		}
	}

	if err := copyWithRename(stagedRoot, targetRoot, renames); err != nil {
		return err
	}

	m.Harness = harnessName
	m.Profile = ctx.ProfileName
	m.TargetRoot = targetRoot
	m.AppliedAt = time.Now()
	maps.Copy(m.Files, newFiles)
	return manifest.Save(ctx.CfgDir, m)
}

// trackAndWriteFile handles manifest check/backup/write for harnesses that write
// a single computed file (e.g. Cursor prepends frontmatter before writing).
// content is the final bytes written to absPath; rel is its path relative to the parent dir.
func trackAndWriteFile(absPath, rel, harnessName string, content []byte, ctx ApplyCtx) error {
	m, err := manifest.Load(ctx.CfgDir, harnessName)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	existing, readErr := os.ReadFile(absPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("reading %s: %w", absPath, readErr)
	}
	if readErr == nil {
		if knownHash, owned := m.Files[rel]; !owned || manifest.HashBytes(existing) != knownHash {
			backupDir, err := backupConflicts([]conflictFile{{rel: rel, abs: absPath}}, harnessName, ctx.CfgDir)
			if err != nil {
				return err
			}
			fmt.Printf("  ! 1 file externally modified — backed up to %s\n", locate.Tilde(backupDir))
			fmt.Printf("      %s\n", rel)
		}
	}

	if err := os.WriteFile(absPath, content, 0o644); err != nil { //nolint:gosec
		return fmt.Errorf("writing %s: %w", absPath, err)
	}

	m.Harness = harnessName
	m.Profile = ctx.ProfileName
	m.TargetRoot = filepath.Dir(absPath)
	m.AppliedAt = time.Now()
	m.Files[rel] = manifest.HashBytes(content)
	return manifest.Save(ctx.CfgDir, m)
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
		if err := os.WriteFile(dst, data, 0o644); err != nil { //nolint:gosec
			return "", fmt.Errorf("backing up %s: %w", c.rel, err)
		}
	}
	return backupDir, nil
}
