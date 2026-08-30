package runstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jophira/weft/internal/privatefile"
)

// countsFileName is the counts cache's basename inside the config dir.
const countsFileName = "counts.json"

// Counts is the cached tally of things weft wants a harness status line to
// show: files sitting in a harness that no source owns, and files two harnesses
// have both changed since the last apply.
//
// It is a cache, deliberately. Both numbers are the product of a filesystem
// sweep across every harness root, and a status line is rendered once per turn,
// so recomputing them on read would put a directory walk in front of every
// prompt. Apply already computes both, so it records them here and the status
// line reads the file.
//
// Unlike RunState this is not tied to a live watcher. Applying without the
// watcher running is the common case, and counts from that apply are just as
// true as counts from a watched one.
type Counts struct {
	// Adoptable is the number of harness-native files no source owns.
	Adoptable int `json:"adoptable"`
	// Conflicts is the number of canonical files held because two or more
	// harnesses diverged on them.
	Conflicts int `json:"conflicts"`
	// Profile names the profile the apply was running, so a status line can tell
	// counts from a different profile apart from counts for the current one.
	Profile string `json:"profile,omitempty"`
	// UpdatedAt is when the apply that produced these numbers finished.
	UpdatedAt time.Time `json:"updated_at"`
}

// Stale reports whether the counts are older than window. A status line showing a
// number from three days ago is worse than showing none: the user acts on it,
// finds nothing there, and stops trusting the line.
func (c Counts) Stale(now time.Time, window time.Duration) bool {
	if c.UpdatedAt.IsZero() {
		return true
	}
	return now.Sub(c.UpdatedAt) > window
}

func countsPathFor(cfgDir string) string {
	return filepath.Join(cfgDir, countsFileName)
}

// WriteCounts records c to cfgDir/counts.json, replacing any previous contents.
// The write is atomic (temp file + rename) so a status line reading concurrently
// never sees a half-written file.
func WriteCounts(cfgDir string, c Counts) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("runstate: marshalling counts: %w", err)
	}
	return privatefile.Write(filepath.Join(cfgDir, countsFileName), data)
}

// ReadCounts returns the cached counts for cfgDir, or nil when none have been
// recorded. A corrupt file is dropped and reported as absent: a cache that
// cannot be parsed is not evidence of anything, and keeping it would fail every
// subsequent read the same way.
func ReadCounts(cfgDir string) (*Counts, error) {
	data, err := os.ReadFile(countsPathFor(cfgDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("runstate: reading counts: %w", err)
	}
	var c Counts
	if err := json.Unmarshal(data, &c); err != nil {
		_ = os.Remove(countsPathFor(cfgDir))
		return nil, nil
	}
	return &c, nil
}

// ClearCounts removes the counts cache. A missing file is not an error.
func ClearCounts(cfgDir string) error {
	err := os.Remove(countsPathFor(cfgDir))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("runstate: removing counts: %w", err)
	}
	return nil
}
