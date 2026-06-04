package harness

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	return os.WriteFile(dst, data, 0o644) //nolint:gosec // dst is always a filepath.Join result — clean by construction
}

// copyWithRename walks stagedRoot and copies each file to targetRoot.
// If a file's relative path appears as a key in renames, the value is used as
// the destination relative path instead — enabling per-harness filename mappings
// (e.g. "CLAUDE.md" → "AGENTS.md" for Codex). Pass nil to copy without renaming.
func copyWithRename(stagedRoot, targetRoot string, renames map[string]string) error {
	return filepath.WalkDir(stagedRoot, func(path string, d fs.DirEntry, err error) error {
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
		fullDst := filepath.Join(targetRoot, dst)
		if err := os.MkdirAll(filepath.Dir(fullDst), 0o755); err != nil {
			return fmt.Errorf("creating parent dir for %s: %w", dst, err)
		}
		return copyFile(path, fullDst)
	})
}
