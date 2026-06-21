package golden

import (
	"bytes"
	"testing"
)

func TestNormalizeEOL_collapsesCRLF(t *testing.T) {
	got := normalizeEOL([]byte("a\r\nb\r\nc"))
	if want := []byte("a\nb\nc"); !bytes.Equal(got, want) {
		t.Errorf("normalizeEOL = %q, want %q", got, want)
	}
}

func TestNormalizeEOL_leavesLFUntouched(t *testing.T) {
	in := []byte("a\nb\nc\n")
	if got := normalizeEOL(in); !bytes.Equal(got, in) {
		t.Errorf("normalizeEOL changed LF-only input: %q", got)
	}
}

func TestAssert_matchesAfterUpdate(t *testing.T) {
	// Round-trip: write via -update path semantics, then assert equality with a
	// CRLF variant to prove OS-independent comparison. We drive Assert directly
	// against a temp golden file by faking the testdata layout in a temp cwd.
	t.Chdir(t.TempDir())

	content := []byte("line1\nline2\n")
	*update = true
	Assert(t, "sample.txt", content)
	*update = false

	// A CRLF-encoded equivalent must still match.
	crlf := bytes.ReplaceAll(content, []byte("\n"), []byte("\r\n"))
	Assert(t, "sample.txt", crlf)
}
