package logger

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// DefaultMaxLogBytes caps one generation of the log.
//
// The cap used to be the whole story: weft logged a single "run" line per
// invocation, so 1 MB held tens of thousands of runs. Now that apply, resolve,
// write-back and conflict events are recorded, a single busy session can write
// more than the old total, which is why Generations exists alongside it.
const DefaultMaxLogBytes = 1 << 20

// DefaultGenerations is how many rotated files are kept besides the active one
// (weft.log.1 … weft.log.5). Five generations at 1 MB is enough to cover the
// window a bug report is usually filed in, without turning the log into a
// database.
const DefaultGenerations = 5

var (
	initOnce sync.Once
	mu       sync.Mutex
	logFile  string
)

// Options configures Init. The zero value is valid and yields the defaults:
// Info level, DefaultMaxLogBytes, DefaultGenerations, and no run ID.
//
// cf. Java: a builder's config object, except Go's zero value replaces the
// need for a builder at all.
type Options struct {
	// Level is the minimum severity written. Anything below is dropped.
	Level slog.Level
	// MaxBytes caps one generation. Zero means DefaultMaxLogBytes.
	MaxBytes int64
	// Generations is how many rotated files to keep. Zero means
	// DefaultGenerations; a negative value keeps none.
	Generations int
	// RunID ties every line from one invocation together. Empty omits the
	// attribute rather than writing a blank one.
	RunID string
}

// LogPath returns the path to the active log file, or "" if Init has not been called.
func LogPath() string {
	mu.Lock()
	defer mu.Unlock()
	return logFile
}

// ParseLevel converts a level name to a slog.Level, reporting whether it was
// recognised. Case-insensitive, so WEFT_LOG_LEVEL=DEBUG and --log-level=debug
// behave the same.
//
// An unknown value is reported rather than silently defaulting, so a typo in
// --log-level is visible instead of quietly leaving the level at Info.
func ParseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info", "":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}

// Init sets the global slog default to write JSON to a rotating log file.
// Silently no-ops on any I/O error so the CLI is never broken by logging.
func Init(version string, opts Options) {
	initOnce.Do(func() {
		path := defaultLogPath()
		mu.Lock()
		logFile = path
		mu.Unlock()

		maxBytes := opts.MaxBytes
		if maxBytes <= 0 {
			maxBytes = DefaultMaxLogBytes
		}
		generations := opts.Generations
		if generations == 0 {
			generations = DefaultGenerations
		}
		if generations < 0 {
			generations = 0
		}

		rw, err := newRotatingWriter(path, maxBytes, generations)
		if err != nil {
			return
		}
		h := slog.NewJSONHandler(rw, &slog.HandlerOptions{Level: opts.Level})
		attrs := []any{
			slog.String("version", version),
			slog.String("platform", runtime.GOOS+"/"+runtime.GOARCH),
		}
		if opts.RunID != "" {
			attrs = append(attrs, slog.String("run_id", opts.RunID))
		}
		slog.SetDefault(slog.New(h).With(attrs...))
	})
}

func defaultLogPath() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "weft", "weft.log")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "weft.log")
	}
	return filepath.Join(home, ".local", "share", "weft", "weft.log")
}

// Tail returns the last n lines of the log file, or nil if unavailable.
func Tail(n int) []byte {
	mu.Lock()
	path := logFile
	mu.Unlock()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	return tailLines(data, n)
}

// tailLines returns the last n newline-terminated lines from data.
func tailLines(data []byte, n int) []byte {
	if n <= 0 || len(data) == 0 {
		return nil
	}
	end := len(data)
	if data[end-1] == '\n' {
		end-- // don't count the trailing newline as a line boundary
	}
	count := 0
	i := end
	for i > 0 && count < n {
		i--
		if data[i] == '\n' {
			count++
		}
	}
	if i == 0 {
		return data // fewer than n lines — return all
	}
	return data[i+1:] // data[i] is the newline before the first returned line
}

// rotatingWriter caps the log at maxSize bytes per generation, shifting
// <path> to <path>.1, <path>.1 to <path>.2 and so on when full, and discarding
// whatever falls off the end.
type rotatingWriter struct {
	mu          sync.Mutex
	path        string
	maxSize     int64
	generations int
	f           *os.File
	size        int64
}

func newRotatingWriter(path string, maxSize int64, generations int) (*rotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	fi, _ := f.Stat()
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	return &rotatingWriter{path: path, maxSize: maxSize, generations: generations, f: f, size: size}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(p)) > w.maxSize {
		w.rotate()
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// Close releases the underlying file handle. Required on Windows, where an open
// handle blocks removal of the containing directory (e.g. a test's t.TempDir
// cleanup). The process-wide logger from Init lives for the process lifetime and
// is never closed.
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// rotate shifts the generation chain up by one and reopens an empty active file.
//
// The shift runs oldest-first so no rename overwrites a file that has not been
// moved out of the way yet. Every error is ignored deliberately: a log that
// cannot rotate must still accept writes, and failing the CLI over it would be
// disproportionate.
func (w *rotatingWriter) rotate() {
	_ = w.f.Close()

	if w.generations <= 0 {
		_ = os.Remove(w.path)
	} else {
		// Drop the oldest, then walk down so .N-1 becomes .N, … , .1 becomes .2.
		_ = os.Remove(w.generationPath(w.generations))
		for i := w.generations - 1; i >= 1; i-- {
			_ = os.Rename(w.generationPath(i), w.generationPath(i+1))
		}
		_ = os.Rename(w.path, w.generationPath(1))
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return
	}
	w.f = f
	w.size = 0
}

// generationPath names the nth rotated file: weft.log.1, weft.log.2, ….
func (w *rotatingWriter) generationPath(n int) string {
	return w.path + "." + strconv.Itoa(n)
}
