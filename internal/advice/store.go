package advice

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// stateFileName is the throttle state's basename inside the config dir. It sits
// next to counts.json, which is the same kind of thing: regenerable cache state
// that exists so a repeated read does not repeat the work behind it.
const stateFileName = "advice.json"

// FileStore records last-shown timestamps in <cfgDir>/advice.json.
//
// Loaded once at construction and written on each MarkShown. Writes are rare
// (at most one per code per throttle window) so there is no batching, and the
// whole map is small enough that rewriting it beats tracking dirty keys.
type FileStore struct {
	mu     sync.Mutex
	path   string
	shown  map[string]time.Time
	loaded bool
}

// NewFileStore returns a store backed by cfgDir/advice.json. A missing or
// corrupt file yields an empty store rather than an error: throttle state is a
// convenience, and refusing to run because a cache would not parse trades a
// small annoyance for a total failure.
func NewFileStore(cfgDir string) *FileStore {
	s := &FileStore{
		path:  filepath.Join(cfgDir, stateFileName),
		shown: map[string]time.Time{},
	}
	s.load()
	return s
}

func (s *FileStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = true
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var raw map[string]time.Time
	if err := json.Unmarshal(data, &raw); err != nil {
		// A cache that cannot be parsed is not evidence of anything. Drop it so
		// the next write starts clean instead of failing the same way forever.
		_ = os.Remove(s.path)
		return
	}
	s.shown = raw
}

// LastShown returns when code was last printed, and whether it ever was.
func (s *FileStore) LastShown(code string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.shown[code]
	return t, ok
}

// MarkShown records that code was printed at t.
func (s *FileStore) MarkShown(code string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shown[code] = t
	return s.flushLocked()
}

// Reset forgets one code, or every code when the argument is empty, so a muted
// or throttled hint can be made to reappear on demand.
func (s *FileStore) Reset(code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if code == "" {
		s.shown = map[string]time.Time{}
	} else {
		delete(s.shown, code)
	}
	return s.flushLocked()
}

// flushLocked writes the map out. Caller holds s.mu.
//
// Atomic via temp file plus rename, matching runstate's counts cache: a
// concurrent watcher and CLI both write this file, and a half-written JSON
// document would be dropped on the next load and lose every timestamp.
func (s *FileStore) flushLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("advice: creating dir: %w", err)
	}
	data, err := json.MarshalIndent(s.shown, "", "  ")
	if err != nil {
		return fmt.Errorf("advice: marshalling state: %w", err)
	}
	tmp, err := os.CreateTemp(dir, stateFileName+".*")
	if err != nil {
		return fmt.Errorf("advice: creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("advice: writing state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("advice: closing temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("advice: setting permissions: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("advice: replacing state: %w", err)
	}
	return nil
}
