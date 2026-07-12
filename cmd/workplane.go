package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jophira/weft/internal/anchor"
)

// workPlaneProjectDir returns the work-plane project directory for a repo,
// keyed by the repo's base name (ADR 0003 — repo-identity resolution reusing the
// Phase-5 convention). e.g. ~/weft/work/projects/weft.
func workPlaneProjectDir(repoAbs string) string {
	home := weftHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "work", "projects", filepath.Base(repoAbs))
}

// workPlaneBundle assembles the repo's project knowledge base — every Markdown
// file under ~/weft/work/projects/<repo>/kb, in path order — into a single
// bundle appended to `weft rules resolve` output. Global anchors ({{weft.home}},
// {{weft.docs}}) are expanded so KB notes can reference weft paths portably.
//
// Returns "" (no error) when there is no KB for the repo — the common case, so
// resolution stays silent for repos without a knowledge base.
func workPlaneBundle(repoAbs string) (string, error) {
	proj := workPlaneProjectDir(repoAbs)
	if proj == "" {
		return "", nil
	}
	kb := filepath.Join(proj, "kb")
	info, err := os.Stat(kb)
	if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspecting %s: %w", kb, err)
	}

	var files []string
	walkErr := filepath.WalkDir(kb, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != kb {
				return fs.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("scanning KB %s: %w", kb, walkErr)
	}
	if len(files) == 0 {
		return "", nil
	}
	sort.Strings(files)

	home, docs := globalAnchors()
	parts := make([]string, 0, len(files)+1)
	parts = append(parts, fmt.Sprintf("# Project knowledge: %s", filepath.Base(repoAbs)))
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // reading user's own KB under the weft home
		if err != nil {
			return "", fmt.Errorf("reading KB file %s: %w", f, err)
		}
		expanded := anchor.Expand(data, anchor.Anchors{Home: home, Docs: docs})
		parts = append(parts, strings.TrimRight(string(expanded), "\n"))
	}
	return strings.Join(parts, "\n\n"), nil
}
