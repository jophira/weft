package watch

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

	once := make(chan struct{})
	return func() {
		select {
		case <-once:
		default:
			close(once)
			close(done)
		}
	}, nil
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
			return nil
		}
		count++
		return nil
	})
	return count, err
}
