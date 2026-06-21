// Package golden provides a tiny golden-file assertion helper shared across
// weft's tests. Golden files live under each package's testdata/golden/ dir.
//
// Run tests with -update to (re)generate golden files:
//
//	go test ./... -update
//
// Comparisons normalise CRLF→LF so golden files authored on any OS match on
// every OS (Windows checkouts and runners included). Tests that emit volatile
// values (absolute paths, timestamps) must scrub them before calling Assert.
package golden

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update is shared by every test binary that imports this package; it is
// registered exactly once because each test binary links the package once.
var update = flag.Bool("update", false, "update golden files instead of comparing")

// Assert compares got against testdata/golden/<name> relative to the calling
// package. With -update it writes the golden file and returns. Line endings are
// normalised to LF on both sides before comparison.
func Assert(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("golden: creating dir for %s: %v", path, err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil { //nolint:gosec // test-only golden path
			t.Fatalf("golden: writing %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden: reading %s (run `go test -update` to create it): %v", path, err)
	}
	if !bytes.Equal(normalizeEOL(got), normalizeEOL(want)) {
		t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// normalizeEOL collapses CRLF to LF so comparisons are OS-independent.
func normalizeEOL(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}
