package manifest

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Manifest records every file weft last wrote for a given harness.
// It is used to distinguish weft-owned files from externally-modified ones.
type Manifest struct {
	Harness    string            `json:"harness"`
	Profile    string            `json:"profile"`
	TargetRoot string            `json:"target_root"`
	AppliedAt  time.Time         `json:"applied_at"`
	Files      map[string]string `json:"files"` // rel path → "sha256:<hex>"
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
