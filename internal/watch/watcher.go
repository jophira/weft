package watch

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
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

	watched := 0
	for _, root := range roots {
		n, addErr := addRecursive(w, root, maxWatchDirs-watched)
		if addErr != nil {
			_ = w.Close()
			return nil, addErr
		}
		watched += n
	}
	if watched == 0 {
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
						_, _ = addRecursive(w, ev.Name, maxWatchDirs)
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

// DebouncedTarget watches dirs for file changes not caused by weft apply writes.
// Events that occur while guard.Active() is true are silently skipped (weft's
// own target writes). After debounce elapses with no further unguarded events,
// fn is called with the accumulated (deduplicated) set of changed paths.
//
// Returns a stop function that shuts down the watcher.
func DebouncedTarget(roots []string, debounce time.Duration, guard *ApplyGuard, fn func([]TargetChange)) (stop func(), err error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating target watcher: %w", err)
	}

	watched := 0
	for _, root := range roots {
		n, addErr := addRecursive(w, root, maxWatchDirs-watched)
		if addErr != nil {
			_ = w.Close()
			return nil, addErr
		}
		watched += n
	}
	if watched == 0 {
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
				// Track new directories so we watch them too.
				if ev.Has(fsnotify.Create) {
					if info, statErr := os.Stat(ev.Name); statErr == nil && info.IsDir() {
						_, _ = addRecursive(w, ev.Name, maxWatchDirs)
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

// addRecursive walks root and registers every non-hidden directory with w up
// to limit directories. Returns an error if limit is exceeded.
func addRecursive(w *fsnotify.Watcher, root string, limit int) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		if count >= limit {
			return fmt.Errorf(
				"source root %q contains more than %d directories — "+
					"make sure your source root points to an AI rules directory, not a large repo or home folder",
				root, maxWatchDirs,
			)
		}
		if addErr := w.Add(path); addErr != nil {
			fmt.Fprintf(os.Stderr, "[weft] watch: skipping %s: %v\n", path, addErr)
			slog.Warn("watch: skipping directory", slog.String("path", path), slog.Any("error", addErr))
			return nil
		}
		count++
		return nil
	})
	return count, err
}
