package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
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

// ── release verification (#276) ──────────────────────────────────────────────

// checksumsServer serves body at /checksums.txt and returns its URL.
func checksumsServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/checksums.txt"
}

// archiveWith writes content to a temp file and returns its path and sha256.
func archiveWith(t *testing.T, content string) (archive, digest string) {
	t.Helper()
	archive = filepath.Join(t.TempDir(), "weft.tar.gz")
	if err := os.WriteFile(archive, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return archive, hex.EncodeToString(sum[:])
}

const asset = "weft_linux_amd64.tar.gz"

func TestVerifyChecksum_acceptsAMatchingDigest(t *testing.T) {
	archive, digest := archiveWith(t, "the real release")
	url := checksumsServer(t, http.StatusOK, digest+"  "+asset+"\n")

	if err := verifyChecksum(archive, url, asset); err != nil {
		t.Errorf("verifyChecksum rejected a matching digest: %v", err)
	}
}

// The case the issue is about: bytes that are not what the release published
// must never reach the binary the user runs.
func TestVerifyChecksum_rejectsATamperedArchive(t *testing.T) {
	_, goodDigest := archiveWith(t, "the real release")
	tampered, _ := archiveWith(t, "malicious payload")
	url := checksumsServer(t, http.StatusOK, goodDigest+"  "+asset+"\n")

	err := verifyChecksum(tampered, url, asset)
	if err == nil {
		t.Fatal("verifyChecksum accepted an archive that does not match the published digest")
	}
	if !strings.Contains(err.Error(), "nothing was installed") {
		t.Errorf("the error should say nothing was installed, got: %v", err)
	}
}

// A release with no checksum for this platform is not a reason to install
// anyway — an unverifiable download is refused, not waved through.
func TestVerifyChecksum_refusesWhenTheAssetIsNotListed(t *testing.T) {
	archive, digest := archiveWith(t, "the real release")
	url := checksumsServer(t, http.StatusOK, digest+"  weft_darwin_arm64.tar.gz\n")

	err := verifyChecksum(archive, url, asset)
	if err == nil {
		t.Fatal("verifyChecksum accepted an archive with no published checksum")
	}
	if !strings.Contains(err.Error(), "unverified") {
		t.Errorf("the error should say the binary is unverified, got: %v", err)
	}
}

// An unreachable or empty checksums.txt fails the update rather than skipping
// verification.
func TestVerifyChecksum_failsWhenChecksumsCannotBeFetched(t *testing.T) {
	archive, _ := archiveWith(t, "the real release")

	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"not found", http.StatusNotFound, "Not Found"},
		{"server error", http.StatusInternalServerError, ""},
		{"empty file", http.StatusOK, ""},
		{"no parsable lines", http.StatusOK, "\n\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url := checksumsServer(t, tc.status, tc.body)
			if err := verifyChecksum(archive, url, asset); err == nil {
				t.Error("verifyChecksum proceeded without a usable checksums file")
			}
		})
	}
}

// goreleaser writes "<digest>  <name>" with two spaces. Blank lines and any
// header must be skipped rather than failing the whole file.
func TestFetchChecksums_parsesTheGoreleaserFormat(t *testing.T) {
	body := "\n" +
		"abc123  weft_linux_amd64.tar.gz\n" +
		"def456  weft_darwin_arm64.tar.gz\n" +
		"not-a-checksum-line\n" +
		"789fed  weft_windows_amd64.zip\n"
	url := checksumsServer(t, http.StatusOK, body)

	got, err := fetchChecksums(url)
	if err != nil {
		t.Fatalf("fetchChecksums: %v", err)
	}
	want := map[string]string{
		"weft_linux_amd64.tar.gz":  "abc123",
		"weft_darwin_arm64.tar.gz": "def456",
		"weft_windows_amd64.zip":   "789fed",
	}
	for name, digest := range want {
		if got[name] != digest {
			t.Errorf("checksums[%q] = %q, want %q", name, got[name], digest)
		}
	}
}

// The asset name the verification looks up must be the one the download URL
// actually names, or every update would fail as "not listed".
func TestVerifyChecksum_assetNameMatchesTheDownloadURL(t *testing.T) {
	name := path.Base(releaseURL("0.2.0"))
	want := fmt.Sprintf("weft_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	if name != want {
		t.Errorf("asset name from the download URL = %q, want %q", name, want)
	}
}
