package autosync

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jophira/weft/internal/source"
)

// DefaultInterval is the minimum time between automatic pulls for a source.
const DefaultInterval = 5 * time.Minute

// State records when each source was last successfully synced.
type State struct {
	Sources map[string]time.Time `json:"sources"`
}

// SyncFunc clones or pulls a source. Returns true when the local tree changed.
type SyncFunc func(s source.Source) (updated bool, err error)

// DefaultStateFilePath returns the path to the sync-state file.
func DefaultStateFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "weft", ".sync_state.json"), nil
}

// ReadState reads the sync state from path.
// Returns an empty State (not an error) when the file does not exist yet.
func ReadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{Sources: make(map[string]time.Time)}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	if s.Sources == nil {
		s.Sources = make(map[string]time.Time)
	}
	return s, nil
}

// WriteState persists s to path, creating parent directories as needed.
func WriteState(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ShouldSync reports whether source name is due for a pull as of now.
// A zero interval means always sync (no debounce).
func ShouldSync(s State, name string, now time.Time, interval time.Duration) bool {
	if interval == 0 {
		return true
	}
	last, ok := s.Sources[name]
	if !ok {
		return true
	}
	return now.Sub(last) > interval
}

// MarkSynced returns a copy of s with name's timestamp set to now.
// The original State is not modified.
func MarkSynced(s State, name string, now time.Time) State {
	out := State{Sources: make(map[string]time.Time, len(s.Sources)+1)}
	for k, v := range s.Sources {
		out.Sources[k] = v
	}
	out.Sources[name] = now
	return out
}

// Run pulls auto_pull sources that are past the debounce interval.
// Uses the real clock; for tests use run directly.
func Run(sources []source.Source, stateFile string, interval time.Duration, syncFn SyncFunc, out io.Writer) error {
	return run(sources, stateFile, interval, time.Now(), syncFn, out)
}

// run is the testable core — now is injected so tests can control time.
// Sync failures are printed to out and do not abort remaining sources.
// Returns the first sync error encountered (if any), after processing all sources.
func run(sources []source.Source, stateFile string, interval time.Duration, now time.Time, syncFn SyncFunc, out io.Writer) error {
	state, err := ReadState(stateFile)
	if err != nil {
		return fmt.Errorf("reading sync state: %w", err)
	}

	var firstErr error
	for _, s := range sources {
		if !s.AutoPull || s.Remote == "" {
			continue
		}
		if !ShouldSync(state, s.Name, now, interval) {
			continue
		}
		updated, err := syncFn(s)
		if err != nil {
			fmt.Fprintf(out, "  auto-sync %q: %v\n", s.Name, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		state = MarkSynced(state, s.Name, now)
		if updated {
			fmt.Fprintf(out, "  ✓ %s updated\n", s.Name)
		}
	}

	_ = WriteState(stateFile, state) // non-fatal: worst case we re-sync next run
	return firstErr
}
