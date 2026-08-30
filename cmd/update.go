package cmd

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jophira/weft/internal/update"
	"github.com/spf13/cobra"
)

var updateIgnore bool

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update weft to the latest release",
	Long:  "Check for and install the latest release of weft. Use --ignore to suppress notices for the current latest version.",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := update.Check(Version)
		if err != nil {
			return fmt.Errorf("checking for updates: %w", err)
		}

		if updateIgnore {
			if result.Latest == "" {
				return fmt.Errorf("could not determine latest version")
			}
			if err := update.IgnoreVersion(result.Latest); err != nil {
				return fmt.Errorf("saving ignore preference: %w", err)
			}
			fmt.Printf("Ignoring v%s — you will be notified again when v%s+1 is released.\n", result.Latest, result.Latest)
			return nil
		}

		if result.Current == "" {
			fmt.Println("weft is a dev build — update is not applicable.")
			return nil
		}
		if !result.Newer {
			fmt.Printf("weft is already up to date (v%s).\n", result.Current)
			return nil
		}

		fmt.Printf("Updating weft v%s → v%s\n", result.Current, result.Latest)
		return doUpdate(result.Latest)
	},
}

func init() {
	updateCmd.Flags().BoolVar(&updateIgnore, "ignore", false, "Ignore this release until a newer one is available")
	rootCmd.AddCommand(updateCmd)
}

func doUpdate(latest string) error {
	if isHomebrew() {
		return runBrew()
	}
	return selfUpdate(latest)
}

func isHomebrew() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	exe = strings.ToLower(filepath.ToSlash(exe))
	return strings.Contains(exe, "cellar") || strings.Contains(exe, "homebrew")
}

func runBrew() error {
	fmt.Println("Detected Homebrew install — running: brew upgrade weft")
	c := exec.Command("brew", "upgrade", "weft")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func selfUpdate(latest string) error {
	if runtime.GOOS == "windows" {
		fmt.Printf("Automatic update is not supported on Windows.\n")
		fmt.Printf("Download the latest release from: %s\n", update.ReleasePageURL(latest))
		return nil
	}

	url := releaseURL(latest)
	fmt.Printf("Downloading %s\n", url)

	tmpDir, err := os.MkdirTemp("", "weft-update-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	archivePath := filepath.Join(tmpDir, "weft.tar.gz")
	if err := downloadFile(url, archivePath); err != nil {
		return fmt.Errorf("downloading release: %w", err)
	}

	// Verified before the archive is opened, not after: this replaces the binary
	// the user is about to run, so nothing that failed its checksum should reach
	// the tar reader, let alone the disk (#276).
	if err := verifyChecksum(archivePath, update.ChecksumsURL(latest), path.Base(url)); err != nil {
		return err
	}

	newBinary := filepath.Join(tmpDir, "weft")
	if err := extractBinary(archivePath, newBinary); err != nil {
		return fmt.Errorf("extracting binary: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving current executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolving symlinks: %w", err)
	}

	if err := os.Chmod(newBinary, 0o755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	// Atomic replace: rename new binary over old (same filesystem assumed).
	// On Linux/macOS the running binary is not locked so this is safe.
	if err := os.Rename(newBinary, exe); err != nil {
		return fmt.Errorf("replacing binary: %w", err)
	}

	fmt.Printf("Updated to v%s. Run `weft version` to confirm.\n", latest)
	return nil
}

// releaseURL is the archive for this platform. The owner and repo come from
// internal/update rather than being spelled out again: the two copies drifted
// once already, and the source repo publishes no releases at all (#250).
func releaseURL(version string) string {
	return update.AssetURL(version, runtime.GOOS, runtime.GOARCH, "tar.gz")
}

const maxDownloadBytes = 100 << 20 // 100 MB

// downloadTimeout bounds the whole exchange, headers and body. http.Get uses
// http.DefaultClient, which has no timeout at all: a remote that accepts the
// connection and then stops sending would hang `weft update` forever.
const downloadTimeout = 5 * time.Minute

// maxChecksumsBytes caps checksums.txt. It is one short line per release
// artifact; anything approaching this is not the file weft asked for.
const maxChecksumsBytes = 1 << 20 // 1 MB

// verifyChecksum fails the update unless archivePath hashes to the digest
// checksums.txt records for assetName.
//
// What this does and does not buy: it catches a corrupted download, a truncated
// transfer, and tampering anywhere between the release and this machine. It
// does not defend against a compromise of the release itself — checksums.txt
// comes from the same place as the archive, so whoever can replace one can
// replace the other. Closing that gap needs the release to be signed, which is
// a change to the release pipeline rather than to this client.
func verifyChecksum(archivePath, checksumsURL, assetName string) error {
	sums, err := fetchChecksums(checksumsURL)
	if err != nil {
		return fmt.Errorf("verifying the download: %w", err)
	}
	want, ok := sums[assetName]
	if !ok {
		return fmt.Errorf(
			"verifying the download: %s publishes no checksum for %s — refusing to install an unverified binary",
			checksumsURL, assetName)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("verifying the download: %w", err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("verifying the download: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf(
			"checksum mismatch for %s:\n  expected %s\n  got      %s\n"+
				"the download does not match what the release publishes — nothing was installed",
			assetName, want, got)
	}
	return nil
}

// fetchChecksums reads a goreleaser checksums.txt into filename → digest. Its
// lines are "<hex digest>  <filename>"; anything that does not parse is skipped
// rather than failing the file, so an added header or blank line is harmless.
func fetchChecksums(url string) (map[string]string, error) {
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(url) //nolint:gosec // URL is constructed from known constant owner/repo, not user input
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s returned %s", url, resp.Status)
	}

	sums := map[string]string{}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, maxChecksumsBytes))
	for sc.Scan() {
		digest, name, found := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if digest == "" || name == "" {
			continue
		}
		sums[name] = digest
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(sums) == 0 {
		return nil, fmt.Errorf("%s held no checksums", url)
	}
	return sums, nil
}

func downloadFile(url, dest string) (retErr error) {
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(url) //nolint:gosec // URL is constructed from known constant owner/repo, not user input
	if err != nil {
		return err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()
	if _, err := io.Copy(f, io.LimitReader(resp.Body, maxDownloadBytes)); err != nil {
		return err
	}
	return nil
}

func extractBinary(archivePath, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && (hdr.Name == "weft" || strings.HasSuffix(hdr.Name, "/weft")) {
			out, err := os.Create(dest)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, io.LimitReader(tr, 50<<20)) // 50 MB cap
			_ = out.Close()
			return copyErr
		}
	}
	return fmt.Errorf("binary not found in archive")
}
