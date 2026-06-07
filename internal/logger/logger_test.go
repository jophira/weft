package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── tailLines ─────────────────────────────────────────────────────────────────

func TestTailLines_nilAndEmpty(t *testing.T) {
	if got := tailLines(nil, 5); got != nil {
		t.Errorf("tailLines(nil, 5) = %q, want nil", got)
	}
	if got := tailLines([]byte{}, 5); got != nil {
		t.Errorf("tailLines(empty, 5) = %q, want nil", got)
	}
}

func TestTailLines_zeroN(t *testing.T) {
	if got := tailLines([]byte("a\nb\n"), 0); got != nil {
		t.Errorf("tailLines(data, 0) = %q, want nil", got)
	}
}

func TestTailLines_fewerLinesThanN(t *testing.T) {
	data := []byte("line1\nline2\n")
	got := tailLines(data, 10)
	if string(got) != string(data) {
		t.Errorf("tailLines(2 lines, 10) = %q, want all data %q", got, data)
	}
}

func TestTailLines_exactlyN(t *testing.T) {
	data := []byte("line1\nline2\nline3\n")
	got := tailLines(data, 3)
	if string(got) != string(data) {
		t.Errorf("tailLines(3 lines, 3) = %q, want %q", got, data)
	}
}

func TestTailLines_moreThanN(t *testing.T) {
	tests := []struct {
		name string
		data string
		n    int
		want string
	}{
		{
			name: "last 2 of 3",
			data: "a\nb\nc\n",
			n:    2,
			want: "b\nc\n",
		},
		{
			name: "last 1 of 4",
			data: "w\nx\ny\nz\n",
			n:    1,
			want: "z\n",
		},
		{
			name: "last 3 of 5",
			data: "1\n2\n3\n4\n5\n",
			n:    3,
			want: "3\n4\n5\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tailLines([]byte(tt.data), tt.n)
			if string(got) != tt.want {
				t.Errorf("tailLines(%q, %d) = %q, want %q", tt.data, tt.n, got, tt.want)
			}
		})
	}
}

func TestTailLines_noTrailingNewline(t *testing.T) {
	data := []byte("a\nb\nc") // no trailing newline
	got := tailLines(data, 2)
	// "b\nc" — last two lines
	if !strings.HasPrefix(string(got), "b\n") {
		t.Errorf("tailLines(no trailing newline, 2) = %q, want prefix b\\n", got)
	}
}

func TestTailLines_singleLine(t *testing.T) {
	data := []byte("only line\n")
	got := tailLines(data, 5)
	if string(got) != string(data) {
		t.Errorf("tailLines(single line, 5) = %q, want %q", got, data)
	}
}

// ── defaultLogPath ─────────────────────────────────────────────────────────────

func TestDefaultLogPath_xdgOverride(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/data")
	got := defaultLogPath()
	want := "/custom/data/weft/weft.log"
	if got != want {
		t.Errorf("defaultLogPath() = %q, want %q", got, want)
	}
}

func TestDefaultLogPath_usesHomeLocalShare(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	got := defaultLogPath()
	if !strings.Contains(got, filepath.Join(".local", "share", "weft", "weft.log")) {
		t.Errorf("defaultLogPath() = %q, expected .local/share/weft/weft.log in path", got)
	}
}

// ── rotatingWriter ────────────────────────────────────────────────────────────

func TestRotatingWriter_writesData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	rw, err := newRotatingWriter(path, 1024)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}

	msg := []byte("hello\n")
	if _, err := rw.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(msg) {
		t.Errorf("log file content = %q, want %q", data, msg)
	}
}

func TestRotatingWriter_rotatesWhenFull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	// Cap at 10 bytes so a 12-byte write triggers rotation.
	rw, err := newRotatingWriter(path, 10)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}

	// First write — fits within the cap.
	if _, err := rw.Write([]byte("123456789\n")); err != nil {
		t.Fatalf("first Write: %v", err)
	}

	// Second write — would exceed cap, must rotate first.
	if _, err := rw.Write([]byte("new entry\n")); err != nil {
		t.Fatalf("second Write: %v", err)
	}

	// Active log should contain only the new entry.
	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile active: %v", err)
	}
	if string(active) != "new entry\n" {
		t.Errorf("active log = %q, want %q", active, "new entry\n")
	}

	// Backup should contain the original entry.
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if string(backup) != "123456789\n" {
		t.Errorf("backup log = %q, want %q", backup, "123456789\n")
	}
}

func TestRotatingWriter_overwritesOldBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	// Write an existing .1 backup to ensure it gets replaced.
	if err := os.WriteFile(path+".1", []byte("stale backup"), 0o640); err != nil {
		t.Fatalf("setup: %v", err)
	}

	rw, err := newRotatingWriter(path, 5)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	// Exceed the cap to trigger rotation.
	_, _ = rw.Write([]byte("123456\n"))
	_, _ = rw.Write([]byte("after\n"))

	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if string(backup) == "stale backup" {
		t.Error("stale backup was not overwritten on rotation")
	}
}

// ── Tail ──────────────────────────────────────────────────────────────────────

func TestTail_noLogFile(t *testing.T) {
	mu.Lock()
	orig := logFile
	logFile = ""
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		logFile = orig
		mu.Unlock()
	})

	if got := Tail(10); got != nil {
		t.Errorf("Tail with no log file = %q, want nil", got)
	}
}

func TestTail_missingFile(t *testing.T) {
	mu.Lock()
	orig := logFile
	logFile = "/does/not/exist/weft.log"
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		logFile = orig
		mu.Unlock()
	})

	if got := Tail(10); got != nil {
		t.Errorf("Tail with missing file = %q, want nil", got)
	}
}

func TestTail_returnsLastNLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weft.log")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mu.Lock()
	orig := logFile
	logFile = path
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		logFile = orig
		mu.Unlock()
	})

	got := Tail(3)
	want := "line3\nline4\nline5\n"
	if string(got) != want {
		t.Errorf("Tail(3) = %q, want %q", got, want)
	}
}
