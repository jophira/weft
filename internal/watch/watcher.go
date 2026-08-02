package watch

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// maxWatchDirs is a safety ceiling on the number of directories watched at once.
// A legitimate AI-rules source root typically contains fewer than 20 directories.
// Hitting this limit almost always means the source root was set to a large tree
// by mistake; we surface a clear error rather than silently exhausting OS limits.
const maxWatchDirs = 500

// Hints appended to the ceiling error, naming the likely cause for each kind of
// root. A source root is user-configured and can genuinely be wrong; a target
// root is derived from the manifest, so the ceiling there means a managed
// subtree grew unexpectedly large.
const (
	sourceHint = "make sure your source root points to an AI rules directory, not a large repo or home folder"
	targetHint = "weft only watches the directories it manages there, so this subtree is unexpectedly large"
)

// Debounced watches roots (and all non-hidden subdirectories within them) and
// calls fn after debounce elapses with no further filesystem events. Roots or
// subdirectories that do not exist are skipped with a warning. Returns an error
// if the total directory count across all roots exceeds maxWatchDirs.
//
// Returns a stop function that shuts down the watcher; fn is invoked from a
// goroutine and must be safe for concurrent use.
func Debounced(roots []string, debounce time.Duration, fn func()) (stop func(), err error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating watcher: %w", err)
	}

	set := newWatchSet(w, "source", sourceHint)
	for _, root := range roots {
		if addErr := set.addTree(root); addErr != nil {
			_ = w.Close()
			return nil, addErr
		}
	}
	if set.len() == 0 {
		_ = w.Close()
		return nil, fmt.Errorf("no watchable directories found")
	}

	done := make(chan struct{})
	go func() {
		defer func() { _ = w.Close() }()
		timer := time.NewTimer(0)
		if !timer.Stop() {
			<-timer.C
		}
		for {
			select {
			case <-done:
				timer.Stop()
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				// When a new directory is created, watch it too (best-effort).
				if ev.Has(fsnotify.Create) {
					if info, statErr := os.Stat(ev.Name); statErr == nil && info.IsDir() {
						_ = set.addTree(ev.Name)
					}
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(debounce)
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			case <-timer.C:
				fn()
			}
		}
	}()

	var once sync.Once
	return func() { once.Do(func() { close(done) }) }, nil
}

// DebouncedFile watches a single file for changes and calls fn after debounce
// elapses with no further events touching it. It watches the file's parent
// directory (non-recursively) and filters events by base name, so it survives
// atomic rewrites (write-temp + rename) that a direct single-file watch would
// miss once the original inode is replaced.
//
// Returns a stop function that shuts down the watcher; fn is invoked from a
// goroutine and must be safe for concurrent use.
func DebouncedFile(path string, debounce time.Duration, fn func()) (stop func(), err error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating file watcher: %w", err)
	}
	if addErr := w.Add(dir); addErr != nil {
		_ = w.Close()
		return nil, fmt.Errorf("watching %s: %w", dir, addErr)
	}

	done := make(chan struct{})
	go func() {
		defer func() { _ = w.Close() }()
		timer := time.NewTimer(0)
		if !timer.Stop() {
			<-timer.C
		}
		for {
			select {
			case <-done:
				timer.Stop()
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Base(ev.Name) != base {
					continue // a sibling file changed — not ours
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(debounce)
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			case <-timer.C:
				fn()
			}
		}
	}()

	var once sync.Once
	return func() { once.Do(func() { close(done) }) }, nil
}

// TargetChange describes a file that was modified externally inside a target directory.
type TargetChange struct {
	Root string // absolute path of the target root
	Rel  string // file path relative to Root
}

// TargetScope names the directories to watch inside one harness target root.
//
// A target root is a live application directory: ~/.claude holds session state,
// plugin caches and per-project history alongside the handful of files weft
// projects into it. Watching it recursively exhausts the OS watch budget and
// reports churn weft can never own, so Dirs lists only the directories that hold
// weft-managed files, plus the ancestors joining them to Root. Build one with
// ScopeForFiles.
type TargetScope struct {
	// Root is the target root. TargetChange.Rel is reported relative to it.
	Root string
	// Dirs are the absolute directories to watch, each Root or below it.
	// Entries that do not exist are skipped. Empty means Root alone.
	Dirs []string
}

// ScopeForFiles builds a TargetScope covering root plus every directory holding
// one of rels (slash- or OS-separated paths relative to root), including the
// intermediate directories that join them to root. Those intermediates matter:
// a directory created later inside a managed subtree — a new skill folder under
// skills/, say — is only picked up if its parent is already watched.
//
// rels that resolve outside root are ignored.
func ScopeForFiles(root string, rels []string) TargetScope {
	root = filepath.Clean(root)
	prefix := root + string(filepath.Separator)
	seen := map[string]struct{}{root: {}}
	dirs := []string{root}
	for _, rel := range rels {
		dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(rel)))
		for dir != root && strings.HasPrefix(dir, prefix) {
			if _, dup := seen[dir]; dup {
				break // this ancestor, and so everything above it, is already in
			}
			seen[dir] = struct{}{}
			dirs = append(dirs, dir)
			dir = filepath.Dir(dir)
		}
	}
	slices.Sort(dirs)
	return TargetScope{Root: root, Dirs: dirs}
}

// DebouncedTarget watches each scope for file changes not caused by weft apply
// writes. Events that occur while guard.Active() is true are silently skipped
// (weft's own target writes). After debounce elapses with no further unguarded
// events, fn is called with the accumulated (deduplicated) set of changed paths.
//
// Returns a stop function that shuts down the watcher.
func DebouncedTarget(scopes []TargetScope, debounce time.Duration, guard *ApplyGuard, fn func([]TargetChange)) (stop func(), err error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating target watcher: %w", err)
	}

	set := newWatchSet(w, "target", targetHint)
	roots := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		roots = append(roots, sc.Root)
		dirs := sc.Dirs
		if len(dirs) == 0 {
			dirs = []string{sc.Root}
		}
		for _, dir := range dirs {
			if set.full() {
				_ = w.Close()
				return nil, fmt.Errorf(
					"target root %q contains more than %d directories — %s",
					sc.Root, maxWatchDirs, targetHint,
				)
			}
			set.add(dir)
		}
	}
	if set.len() == 0 {
		_ = w.Close()
		return nil, fmt.Errorf("no watchable target directories found")
	}

	pending := map[string]TargetChange{} // key = "root:rel" for deduplication
	done := make(chan struct{})
	go func() {
		defer func() { _ = w.Close() }()
		timer := time.NewTimer(0)
		if !timer.Stop() {
			<-timer.C
		}
		for {
			select {
			case <-done:
				timer.Stop()
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				// Track new directories so we watch them too — but only the ones
				// inside the scope (see expandTargetScope).
				if ev.Has(fsnotify.Create) {
					if info, statErr := os.Stat(ev.Name); statErr == nil && info.IsDir() {
						expandTargetScope(set, roots, ev.Name)
						continue
					}
				}
				// Ignore events caused by weft's own apply writes.
				if guard.Active() {
					continue
				}
				root, rel, found := targetRoot(roots, ev.Name)
				if !found {
					continue
				}
				pending[root+":"+rel] = TargetChange{Root: root, Rel: rel}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(debounce)
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			case <-timer.C:
				if len(pending) > 0 {
					changes := make([]TargetChange, 0, len(pending))
					for _, c := range pending {
						changes = append(changes, c)
					}
					pending = map[string]TargetChange{}
					fn(changes)
				}
			}
		}
	}()

	var once sync.Once
	return func() { once.Do(func() { close(done) }) }, nil
}

// expandTargetScope starts watching dir, a directory created after the watcher
// started, but only when it sits inside a directory that already holds
// weft-managed content — a new skill folder under skills/, say.
//
// It deliberately refuses to expand into a direct child of a target root. A
// harness home spawns state directories continuously (~/.claude/projects,
// plugins/, session-env/), and following those is exactly what makes watching a
// live harness root exhaust the OS watch budget. Such a directory only becomes
// watchable once weft manages a file inside it, which puts it in the scope the
// next time the watchers are built.
func expandTargetScope(set *watchSet, roots []string, dir string) {
	parent := filepath.Dir(dir)
	if !set.has(parent) {
		return // outside the scope entirely
	}
	if slices.Contains(roots, parent) {
		return // harness state directory, not weft's
	}
	if err := set.addTree(dir); err != nil {
		fmt.Fprintf(os.Stderr, "[weft] watch: %v\n", err)
		slog.Warn("watch: scope expansion hit the directory ceiling",
			slog.String("dir", dir), slog.Any("error", err))
	}
}

// targetRoot finds which root contains absPath and returns the root and relative path.
// Returns ok=false when absPath is not under any of the watched roots.
func targetRoot(roots []string, absPath string) (root, rel string, ok bool) {
	for _, r := range roots {
		if absPath == r || strings.HasPrefix(absPath, r+string(filepath.Separator)) {
			rel, err := filepath.Rel(r, absPath)
			if err == nil {
				return r, rel, true
			}
		}
	}
	return "", "", false
}

// watchSet registers directories with an fsnotify watcher, deduplicating them
// and enforcing maxWatchDirs across every root it is asked to cover. kind and
// hint only shape the ceiling error, which names the root that overran it.
type watchSet struct {
	w     *fsnotify.Watcher
	kind  string // "source" or "target"
	hint  string
	limit int
	dirs  map[string]struct{}
}

func newWatchSet(w *fsnotify.Watcher, kind, hint string) *watchSet {
	return &watchSet{w: w, kind: kind, hint: hint, limit: maxWatchDirs, dirs: map[string]struct{}{}}
}

func (s *watchSet) len() int { return len(s.dirs) }

func (s *watchSet) full() bool { return len(s.dirs) >= s.limit }

func (s *watchSet) has(dir string) bool {
	_, ok := s.dirs[dir]
	return ok
}

// add registers dir, unless it is already watched, has vanished, or the OS
// refuses it. Reports whether dir is watched once add returns. Callers must
// check full() first — add does not enforce the ceiling itself.
func (s *watchSet) add(dir string) bool {
	if s.has(dir) {
		return true
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false // gone, or not a directory
	}
	if addErr := s.w.Add(dir); addErr != nil {
		fmt.Fprintf(os.Stderr, "[weft] watch: skipping %s: %v\n", dir, addErr)
		slog.Warn("watch: skipping directory", slog.String("path", dir), slog.Any("error", addErr))
		return false
	}
	s.dirs[dir] = struct{}{}
	return true
}

// addTree walks root and registers every non-hidden directory within it.
// Returns an error when doing so would push the set past maxWatchDirs.
func (s *watchSet) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable paths
		}
		if !d.IsDir() {
			return nil
		}
		// Skip hidden directories (except the root itself, which may start with ".").
		if path != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if s.full() {
			return fmt.Errorf(
				"%s root %q contains more than %d directories — %s",
				s.kind, root, s.limit, s.hint,
			)
		}
		s.add(path)
		return nil
	})
}
