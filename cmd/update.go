package cmd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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
		fmt.Printf("Download the latest release from: https://github.com/jophira/weft/releases/tag/v%s\n", latest)
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

func releaseURL(version string) string {
	os_ := runtime.GOOS
	arch := runtime.GOARCH
	return fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/v%s/weft_%s_%s.tar.gz",
		"jophira", "weft", version, os_, arch,
	)
}

func downloadFile(url, dest string) (retErr error) {
	resp, err := http.Get(url) //nolint:gosec // URL is constructed from known constant owner/repo, not user input
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
	_, err = io.Copy(f, resp.Body)
	return err
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
