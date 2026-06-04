package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const checkInterval = 24 * time.Hour

const (
	repoOwner = "jophira"
	repoName  = "weft"
)

// Cache is persisted to ~/.config/weft/.update_check.json.
type Cache struct {
	CheckedAt      time.Time `json:"checked_at"`
	Latest         string    `json:"latest"`
	IgnoredVersion string    `json:"ignored_version,omitempty"`
}

// Result is returned by Check and CheckWith.
type Result struct {
	Latest  string
	Current string
	Newer   bool
}

// CheckOptions injects dependencies into CheckWith.
// Zero values fall back to production defaults.
type CheckOptions struct {
	// CachePath overrides the default ~/.config/weft/.update_check.json.
	CachePath string
	// Now is the reference time used for cache-expiry decisions.
	Now time.Time
	// Fetch overrides the default GitHub API call.
	Fetch func() (string, error)
}

func CacheFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "weft", ".update_check.json"), nil
}

func ReadCache(path string) (Cache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Cache{}, err
	}
	var c Cache
	return c, json.Unmarshal(data, &c)
}

func WriteCache(path string, c Cache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func fetchLatest() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}
	var info struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	return strings.TrimPrefix(info.TagName, "v"), nil
}

// IgnoreVersion stores version as ignored so Check returns Newer=false until a
// later release ships.
func IgnoreVersion(version string) error {
	path, err := CacheFilePath()
	if err != nil {
		return err
	}
	c, _ := ReadCache(path)
	c.IgnoredVersion = strings.TrimPrefix(version, "v")
	return WriteCache(path, c)
}

// CheckWith is the testable core: callers inject cache path, reference time,
// and fetch function via opts. Zero fields fall back to production defaults.
func CheckWith(currentVersion string, opts CheckOptions) (Result, error) {
	if strings.HasSuffix(currentVersion, "-dev") || os.Getenv("CI") != "" {
		return Result{}, nil
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	fetch := opts.Fetch
	if fetch == nil {
		fetch = fetchLatest
	}
	cachePath := opts.CachePath
	if cachePath == "" {
		var err error
		cachePath, err = CacheFilePath()
		if err != nil {
			return Result{}, err
		}
	}

	current := strings.TrimPrefix(currentVersion, "v")

	cache, err := ReadCache(cachePath)
	if err != nil || now.Sub(cache.CheckedAt) > checkInterval {
		latest, err := fetch()
		if err != nil {
			return Result{}, err
		}
		// Preserve IgnoredVersion across a fresh fetch.
		cache = Cache{
			CheckedAt:      now,
			Latest:         latest,
			IgnoredVersion: cache.IgnoredVersion,
		}
		_ = WriteCache(cachePath, cache)
	}

	latest := strings.TrimPrefix(cache.Latest, "v")
	ignored := strings.TrimPrefix(cache.IgnoredVersion, "v")

	if latest == ignored {
		return Result{Latest: latest, Current: current, Newer: false}, nil
	}

	return Result{
		Latest:  latest,
		Current: current,
		Newer:   isNewer(latest, current),
	}, nil
}

// Check returns whether a newer release is available, using production defaults.
func Check(currentVersion string) (Result, error) {
	return CheckWith(currentVersion, CheckOptions{})
}

func isNewer(candidate, current string) bool {
	if candidate == current {
		return false
	}
	return compareSemver(candidate, current) > 0
}

func compareSemver(a, b string) int {
	av := parseSemver(a)
	bv := parseSemver(b)
	for i := range av {
		if av[i] > bv[i] {
			return 1
		}
		if av[i] < bv[i] {
			return -1
		}
	}
	return 0
}

func parseSemver(v string) [3]int {
	v = strings.SplitN(v, "-", 2)[0]
	v = strings.SplitN(v, "+", 2)[0]
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		fmt.Sscanf(p, "%d", &out[i]) //nolint:errcheck
	}
	return out
}
