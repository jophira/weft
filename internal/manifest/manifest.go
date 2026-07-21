package manifest

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Manifest records every file weft last wrote for a given harness.
// It is used to distinguish weft-owned files from externally-modified ones.
type Manifest struct {
	Harness    string    `json:"harness"`
	Profile    string    `json:"profile"`
	TargetRoot string    `json:"target_root"`
	AppliedAt  time.Time `json:"applied_at"`
	// Files is the durable ownership record: every path weft has written for this
	// harness, mapped to the sha256 of the bytes it last wrote. Entries survive a
	// file leaving the active profile so weft can still recognise its own output
	// (and detect genuine external edits) if that file is projected again later.
	Files map[string]string `json:"files"` // rel path -> "sha256:<hex>"
	// Staged is the set of paths the last apply actually projected — a subset of
	// Files. The difference between the two is what a profile switch dropped, which
	// is how apply knows to remove files that are no longer part of the profile.
	Staged      []string            `json:"staged,omitempty"`
	SourceFiles map[string][]string `json:"source_files,omitempty"` // rel path -> ordered source names (AppendStrategy files only)
	// InstructionPath is the absolute path of the harness root instruction file
	// (CLAUDE.md, AGENTS.md, …) in which weft manages a <!-- weft:begin/end -->
	// block. Empty for harnesses with no instruction file (e.g. Warp).
	InstructionPath string `json:"instruction_path,omitempty"`
	// InstructionBlock is the sha256 of the managed block body weft last wrote.
	// Unlike Files (whole-file ownership), weft owns only the block — content
	// outside it is the user's. Write-back compares the on-disk block to this
	// hash to detect external edits.
	InstructionBlock string `json:"instruction_block,omitempty"`
}

func manifestPath(cfgDir, harnessName string) string {
	return filepath.Join(cfgDir, "manifests", harnessName+".json")
}

// Load reads the manifest for harnessName from cfgDir.
// Returns an empty manifest (not an error) when none exists yet.
func Load(cfgDir, harnessName string) (*Manifest, error) {
	data, err := os.ReadFile(manifestPath(cfgDir, harnessName))
	if os.IsNotExist(err) {
		return &Manifest{Harness: harnessName, Files: map[string]string{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading manifest for %s: %w", harnessName, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest for %s: %w", harnessName, err)
	}
	if m.Files == nil {
		m.Files = map[string]string{}
	}
	return &m, nil
}

// IsSidecarKey reports whether key is a sentinel Files key for a file weft tracks
// outside the harness target root, rather than a path relative to it.
//
// Sidecar keys carry a "<class>:" prefix and embed an absolute path — see
// harness.mcpManifestKey, which keys ~/.claude.json as "mcp:/home/you/.claude.json"
// because that file is a sibling of the target root, not a child of it. A staged
// key is always a slash-separated path *relative* to the target root and so can
// never contain a colon, which makes the colon an unambiguous discriminator on
// every OS (a Windows drive letter only appears in the absolute path a sidecar
// key embeds, never in a staged key).
//
// Callers that resolve keys to real paths must skip these: joining one onto the
// target root yields a nonsense path that merely fails to exist on Unix but is
// outright invalid on Windows.
func IsSidecarKey(key string) bool {
	return strings.ContainsRune(key, ':')
}

// StagedSet returns the paths the last apply projected, as a set for lookup.
//
// Manifests written before Staged existed have no such field. In those the Files
// map was replaced wholesale on every apply, so its keys *are* the last staged
// set — using them as the fallback makes the first apply after an upgrade behave
// exactly as it did before, with no spurious deletions.
func (m *Manifest) StagedSet() map[string]struct{} {
	keys := m.Staged
	if keys == nil {
		keys = make([]string, 0, len(m.Files))
		for rel := range m.Files {
			// Sidecar entries were never staged, so the fallback must not
			// reintroduce them: apply would then see one as dropped and try to
			// resolve the sentinel as a path under the target root.
			if IsSidecarKey(rel) {
				continue
			}
			keys = append(keys, rel)
		}
	}
	set := make(map[string]struct{}, len(keys))
	for _, rel := range keys {
		set[rel] = struct{}{}
	}
	return set
}

// Save writes m to cfgDir/manifests/<harness>.json, creating the directory if needed.
func Save(cfgDir string, m *Manifest) error {
	p := manifestPath(cfgDir, m.Harness)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("creating manifests dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("serialising manifest: %w", err)
	}
	return os.WriteFile(p, data, 0o644) //nolint:gosec // path is derived from config dir, not user input
}

// HashFile returns the sha256 hex digest of the file at path.
func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", path, err)
	}
	return HashBytes(data), nil
}

// HashBytes returns the sha256 hex digest of data.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum)
}
