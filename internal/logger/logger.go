package logger

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const maxLogBytes = 1 << 20 // 1 MB

var (
	initOnce sync.Once
	mu       sync.Mutex
	logFile  string
)

// LogPath returns the path to the active log file, or "" if Init has not been called.
func LogPath() string {
	mu.Lock()
	defer mu.Unlock()
	return logFile
}

// Init sets the global slog default to write JSON to a rotating log file.
// Silently no-ops on any I/O error so the CLI is never broken by logging.
func Init(version string) {
	initOnce.Do(func() {
		path := defaultLogPath()
		mu.Lock()
		logFile = path
		mu.Unlock()

		rw, err := newRotatingWriter(path, maxLogBytes)
		if err != nil {
			return
		}
		h := slog.NewJSONHandler(rw, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(h).With(
			slog.String("version", version),
			slog.String("platform", runtime.GOOS+"/"+runtime.GOARCH),
		))
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

// rotatingWriter caps the log at maxSize bytes, renaming the active file to
// <path>.1 when full (keeping at most two files on disk at once).
type rotatingWriter struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	f       *os.File
	size    int64
}

func newRotatingWriter(path string, maxSize int64) (*rotatingWriter, error) {
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
	return &rotatingWriter{path: path, maxSize: maxSize, f: f, size: size}, nil
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

func (w *rotatingWriter) rotate() {
	_ = w.f.Close()
	backup := w.path + ".1"
	_ = os.Remove(backup)
	_ = os.Rename(w.path, backup)
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return
	}
	w.f = f
	w.size = 0
}
