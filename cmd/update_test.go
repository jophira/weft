package cmd

import (
	"runtime"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/update"
)

// TestReleaseURL_usesTheReleaseRepo is the cmd-side half of #250. The bug was a
// second copy of the owner and repo living here, spelled "jophira/weft", while
// goreleaser published to jophira/weft-releases. Asserting the delegation rather
// than the literal string means re-hardcoding it fails the test.
func TestReleaseURL_usesTheReleaseRepo(t *testing.T) {
	got := releaseURL("0.2.0")
	want := update.AssetURL("0.2.0", runtime.GOOS, runtime.GOARCH, "tar.gz")
	if got != want {
		t.Errorf("releaseURL = %q, want %q", got, want)
	}
	if strings.Contains(got, "/jophira/weft/") {
		t.Errorf("releaseURL = %q, which points at the source repo — it publishes no releases", got)
	}
}
