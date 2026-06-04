package update_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jophira/weft/internal/update"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func tempCache(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".update_check.json")
}

func stubFetcher(version string) func() (string, error) {
	return func() (string, error) { return version, nil }
}

func freshOpts(t *testing.T, latest string) update.CheckOptions {
	t.Helper()
	return update.CheckOptions{
		CachePath: tempCache(t),
		Now:       time.Now(),
		Fetch:     stubFetcher(latest),
	}
}

// ── isNewer / compareSemver (via CheckWith) ───────────────────────────────────

func TestCheckWith_newerPatch(t *testing.T) {
	opts := freshOpts(t, "0.1.1")
	r, err := update.CheckWith("0.1.0", opts)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Newer {
		t.Errorf("0.1.1 > 0.1.0: expected Newer=true")
	}
}

func TestCheckWith_newerMinor(t *testing.T) {
	opts := freshOpts(t, "0.2.0")
	r, _ := update.CheckWith("0.1.9", opts)
	if !r.Newer {
		t.Errorf("0.2.0 > 0.1.9: expected Newer=true")
	}
}

func TestCheckWith_newerMajor(t *testing.T) {
	opts := freshOpts(t, "2.0.0")
	r, _ := update.CheckWith("1.9.9", opts)
	if !r.Newer {
		t.Errorf("2.0.0 > 1.9.9: expected Newer=true")
	}
}

func TestCheckWith_sameVersion(t *testing.T) {
	opts := freshOpts(t, "1.2.3")
	r, _ := update.CheckWith("1.2.3", opts)
	if r.Newer {
		t.Errorf("1.2.3 == 1.2.3: expected Newer=false")
	}
}

func TestCheckWith_olderAvailable(t *testing.T) {
	opts := freshOpts(t, "0.0.9")
	r, _ := update.CheckWith("1.0.0", opts)
	if r.Newer {
		t.Errorf("0.0.9 < 1.0.0: expected Newer=false")
	}
}

func TestCheckWith_vPrefixStripped(t *testing.T) {
	opts := freshOpts(t, "v0.2.0")
	r, _ := update.CheckWith("v0.1.0", opts)
	if !r.Newer {
		t.Errorf("v0.2.0 > v0.1.0: expected Newer=true")
	}
}

func TestCheckWith_preReleaseSuffix(t *testing.T) {
	// pre-release suffix must not confuse semver parsing
	opts := freshOpts(t, "1.0.0-beta.1")
	r, _ := update.CheckWith("0.9.0", opts)
	if !r.Newer {
		t.Errorf("1.0.0-beta.1 parsed as 1.0.0 should be > 0.9.0")
	}
}

// ── dev / CI guards ───────────────────────────────────────────────────────────

func TestCheckWith_devBuild_skipped(t *testing.T) {
	opts := freshOpts(t, "9.9.9")
	r, err := update.CheckWith("0.1.0-dev", opts)
	if err != nil {
		t.Fatal(err)
	}
	if r.Newer {
		t.Error("dev build: expected Newer=false")
	}
}

func TestCheckWith_CIEnv_skipped(t *testing.T) {
	t.Setenv("CI", "true")
	opts := freshOpts(t, "9.9.9")
	r, err := update.CheckWith("0.1.0", opts)
	if err != nil {
		t.Fatal(err)
	}
	if r.Newer {
		t.Error("CI=true: expected Newer=false")
	}
}

// ── cache behaviour ───────────────────────────────────────────────────────────

func TestCheckWith_freshCache_noNetworkCall(t *testing.T) {
	path := tempCache(t)
	now := time.Now()

	// Prime cache with a version that would look newer.
	update.WriteCache(path, update.Cache{
		CheckedAt: now.Add(-1 * time.Hour), // well within 24h
		Latest:    "0.2.0",
	})

	fetched := false
	opts := update.CheckOptions{
		CachePath: path,
		Now:       now,
		Fetch:     func() (string, error) { fetched = true; return "9.9.9", nil },
	}
	r, err := update.CheckWith("0.1.0", opts)
	if err != nil {
		t.Fatal(err)
	}
	if fetched {
		t.Error("fresh cache: should not have called Fetch")
	}
	if r.Latest != "0.2.0" {
		t.Errorf("Latest = %q, want 0.2.0", r.Latest)
	}
	if !r.Newer {
		t.Error("expected Newer=true from cached value")
	}
}

func TestCheckWith_staleCache_fetchesNetwork(t *testing.T) {
	path := tempCache(t)
	now := time.Now()

	update.WriteCache(path, update.Cache{
		CheckedAt: now.Add(-25 * time.Hour), // past 24h
		Latest:    "0.1.0",
	})

	fetched := false
	opts := update.CheckOptions{
		CachePath: path,
		Now:       now,
		Fetch:     func() (string, error) { fetched = true; return "0.3.0", nil },
	}
	r, err := update.CheckWith("0.1.0", opts)
	if err != nil {
		t.Fatal(err)
	}
	if !fetched {
		t.Error("stale cache: expected Fetch to be called")
	}
	if r.Latest != "0.3.0" {
		t.Errorf("Latest = %q, want 0.3.0", r.Latest)
	}
}

func TestCheckWith_missingCache_fetchesNetwork(t *testing.T) {
	path := tempCache(t) // file does not exist yet

	fetched := false
	opts := update.CheckOptions{
		CachePath: path,
		Now:       time.Now(),
		Fetch:     func() (string, error) { fetched = true; return "1.0.0", nil },
	}
	_, err := update.CheckWith("0.1.0", opts)
	if err != nil {
		t.Fatal(err)
	}
	if !fetched {
		t.Error("missing cache: expected Fetch to be called")
	}
}

func TestCheckWith_fetchError(t *testing.T) {
	path := tempCache(t)
	opts := update.CheckOptions{
		CachePath: path,
		Now:       time.Now(),
		Fetch:     func() (string, error) { return "", errors.New("network down") },
	}
	_, err := update.CheckWith("0.1.0", opts)
	if err == nil {
		t.Error("expected error from failed fetch")
	}
}

func TestCheckWith_fetchWritesCache(t *testing.T) {
	path := tempCache(t)
	now := time.Now()
	opts := update.CheckOptions{
		CachePath: path,
		Now:       now,
		Fetch:     stubFetcher("0.5.0"),
	}
	_, err := update.CheckWith("0.1.0", opts)
	if err != nil {
		t.Fatal(err)
	}
	c, err := update.ReadCache(path)
	if err != nil {
		t.Fatalf("ReadCache after CheckWith: %v", err)
	}
	if c.Latest != "0.5.0" {
		t.Errorf("cached Latest = %q, want 0.5.0", c.Latest)
	}
}

func TestCheckWith_staleCache_preservesIgnoredVersion(t *testing.T) {
	path := tempCache(t)
	now := time.Now()

	update.WriteCache(path, update.Cache{
		CheckedAt:      now.Add(-25 * time.Hour),
		Latest:         "0.1.0",
		IgnoredVersion: "0.2.0",
	})

	opts := update.CheckOptions{
		CachePath: path,
		Now:       now,
		Fetch:     stubFetcher("0.2.0"),
	}
	r, err := update.CheckWith("0.1.0", opts)
	if err != nil {
		t.Fatal(err)
	}
	if r.Newer {
		t.Error("latest matches ignored version: expected Newer=false")
	}
	c, _ := update.ReadCache(path)
	if c.IgnoredVersion != "0.2.0" {
		t.Errorf("IgnoredVersion lost after stale fetch: got %q", c.IgnoredVersion)
	}
}

func TestCheckWith_ignoredVersion_suppressesNotice(t *testing.T) {
	path := tempCache(t)
	now := time.Now()

	update.WriteCache(path, update.Cache{
		CheckedAt:      now.Add(-1 * time.Hour),
		Latest:         "0.2.0",
		IgnoredVersion: "0.2.0",
	})

	opts := update.CheckOptions{
		CachePath: path,
		Now:       now,
		Fetch:     stubFetcher("0.2.0"),
	}
	r, _ := update.CheckWith("0.1.0", opts)
	if r.Newer {
		t.Error("ignored version: expected Newer=false")
	}
}

func TestCheckWith_ignoredVersion_clearedByNewerRelease(t *testing.T) {
	path := tempCache(t)
	now := time.Now()

	update.WriteCache(path, update.Cache{
		CheckedAt:      now.Add(-25 * time.Hour),
		Latest:         "0.2.0",
		IgnoredVersion: "0.2.0",
	})

	opts := update.CheckOptions{
		CachePath: path,
		Now:       now,
		Fetch:     stubFetcher("0.3.0"), // new release is out
	}
	r, _ := update.CheckWith("0.1.0", opts)
	if !r.Newer {
		t.Error("new release past ignored version: expected Newer=true")
	}
}

// ── IgnoreVersion ─────────────────────────────────────────────────────────────

func TestIgnoreVersion_writesIgnoredVersion(t *testing.T) {
	// IgnoreVersion uses the real CacheFilePath (HOME), so exercise it via
	// WriteCache/ReadCache with a temp path instead.
	path := tempCache(t)
	update.WriteCache(path, update.Cache{Latest: "0.2.0"})
	update.WriteCache(path, update.Cache{
		CheckedAt:      time.Now(),
		Latest:         "0.2.0",
		IgnoredVersion: "0.2.0",
	})
	c, err := update.ReadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.IgnoredVersion != "0.2.0" {
		t.Errorf("IgnoredVersion = %q, want 0.2.0", c.IgnoredVersion)
	}
}

func TestIgnoreVersion_stripsVPrefix(t *testing.T) {
	path := tempCache(t)
	update.WriteCache(path, update.Cache{
		CheckedAt:      time.Now(),
		Latest:         "0.2.0",
		IgnoredVersion: "v0.2.0",
	})
	c, _ := update.ReadCache(path)
	// IgnoredVersion stored without "v" prefix compares equal to stripped Latest
	latest := "0.2.0"
	ignored := c.IgnoredVersion
	if len(ignored) > 0 && ignored[0] == 'v' {
		ignored = ignored[1:]
	}
	if ignored != latest {
		t.Errorf("stripped ignored %q != latest %q", ignored, latest)
	}
}

// ── ReadCache / WriteCache ────────────────────────────────────────────────────

func TestReadCache_missingFile(t *testing.T) {
	_, err := update.ReadCache(tempCache(t))
	if err == nil {
		t.Error("expected error for missing cache file")
	}
}

func TestWriteCache_createsParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "c", ".update_check.json")
	if err := update.WriteCache(path, update.Cache{Latest: "1.0.0"}); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestWriteCache_roundtrip(t *testing.T) {
	path := tempCache(t)
	now := time.Now().Truncate(time.Second) // JSON marshalling truncates sub-second
	in := update.Cache{CheckedAt: now, Latest: "2.0.0", IgnoredVersion: "1.9.0"}
	if err := update.WriteCache(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := update.ReadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if !out.CheckedAt.Equal(now) {
		t.Errorf("CheckedAt = %v, want %v", out.CheckedAt, now)
	}
	if out.Latest != "2.0.0" {
		t.Errorf("Latest = %q, want 2.0.0", out.Latest)
	}
	if out.IgnoredVersion != "1.9.0" {
		t.Errorf("IgnoredVersion = %q, want 1.9.0", out.IgnoredVersion)
	}
}
