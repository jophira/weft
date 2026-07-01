package rules

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC) }

// javaCtx is a repo context matching the std tree's java rule but not springboot.
func javaCtx() Context {
	return Context{Files: []string{"pom.xml"}, Deps: []string{"junit"}}
}

func TestFingerprint_StableAndSensitive(t *testing.T) {
	root := stdRulesTree(t)
	fp1, err := Fingerprint(root)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	fp2, err := Fingerprint(root)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if fp1 != fp2 {
		t.Errorf("fingerprint not stable: %s vs %s", fp1, fp2)
	}

	// Changing a rule file's size must change the fingerprint.
	writeFile(t, root, "common.md", "---\nlabel: common\ndetect: \"true\"\npriority: 0\n---\nCOMMON CHANGED")
	fp3, err := Fingerprint(root)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if fp3 == fp1 {
		t.Error("fingerprint should change after a rule edit")
	}

	// A non-.md sibling (e.g. the cache itself) must not affect the fingerprint.
	writeFile(t, root, CacheFileName, "meta: {}\n")
	fp4, err := Fingerprint(root)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if fp4 != fp3 {
		t.Error("non-.md files must not affect the fingerprint")
	}
}

func TestResolveWithCache_HitMatchesNoCache(t *testing.T) {
	root := stdRulesTree(t)
	ev := newEvaluator(t)
	ctx := javaCtx()

	// Prime the cache.
	cache, _, err := BuildCache(root, fixedNow())
	if err != nil {
		t.Fatalf("BuildCache: %v", err)
	}
	if err := cache.Save(DefaultCachePath(root)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cached, status, err := ResolveWithCache(root, ctx, ev, CacheOptions{})
	if err != nil {
		t.Fatalf("ResolveWithCache: %v", err)
	}
	if !status.Used {
		t.Errorf("expected a cache hit, got status %+v", status)
	}

	direct, err := Resolve(root, ctx, ev)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got, want := loadedLabels(cached), loadedLabels(direct); !equalStrings(got, want) {
		t.Errorf("cache-hit labels %v != no-cache labels %v", got, want)
	}
	if cached.Bundle() != direct.Bundle() {
		t.Errorf("cache-hit bundle differs from no-cache bundle")
	}
}

func TestResolveWithCache_AbsentFallbackWritesCache(t *testing.T) {
	root := stdRulesTree(t)
	ctx := javaCtx()
	cachePath := DefaultCachePath(root)
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("expected no cache initially")
	}

	res, status, err := ResolveWithCache(root, ctx, newEvaluator(t), CacheOptions{Now: fixedNow})
	if err != nil {
		t.Fatalf("ResolveWithCache: %v", err)
	}
	if status.Used {
		t.Error("absent cache must not report a hit")
	}
	if !status.Wrote {
		t.Error("absent cache should be written on fallback")
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("expected cache file written: %v", err)
	}
	want := []string{"common", "common-backend", "java"}
	if got := loadedLabels(res); !equalStrings(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
}

// TestResolveWithCache_StaleRebuildSelfHeals proves the central invariant:
// editing a rule after the cache was built yields correct (new) output without
// any manual rebuild, and marks the cache stale-rebuilt.
func TestResolveWithCache_StaleRebuildSelfHeals(t *testing.T) {
	root := stdRulesTree(t)
	ev := newEvaluator(t)
	ctx := javaCtx()

	cache, _, err := BuildCache(root, fixedNow())
	if err != nil {
		t.Fatalf("BuildCache: %v", err)
	}
	if err := cache.Save(DefaultCachePath(root)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Edit java.md so it now also requires a spring dependency we don't have,
	// changing the selection — and force a distinct mtime.
	javaPath := writeFile(t, root, "java/java.md", "---\nlabel: java\ndetect: \"false\"\nextends: [common-backend]\npriority: 20\n---\nJAVA EDITED")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(javaPath, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	res, status, err := ResolveWithCache(root, ctx, ev, CacheOptions{Now: fixedNow})
	if err != nil {
		t.Fatalf("ResolveWithCache: %v", err)
	}
	if !status.StaleRebuilt {
		t.Errorf("expected StaleRebuilt, got %+v", status)
	}
	// java's detect is now false, so neither java nor its dependents load.
	if contains(loadedLabels(res), "java") {
		t.Errorf("edited java (detect:false) must not load; got %v", loadedLabels(res))
	}

	// The cache on disk must now be fresh for the new fingerprint.
	reloaded, err := LoadCache(DefaultCachePath(root))
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	fp, _ := Fingerprint(root)
	if !reloaded.fresh(fp) {
		t.Error("cache was not rewritten fresh after self-heal")
	}
}

func TestResolveWithCache_DisabledBypasses(t *testing.T) {
	root := stdRulesTree(t)
	// Write a deliberately wrong cache to prove --no-cache ignores it.
	bogus := Cache{Labels: map[string]cacheEntry{"common": {File: "common.md", BodyHash: "x"}}}
	bogus.Meta.SourceFingerprint = "sha256:stale"
	if err := bogus.Save(DefaultCachePath(root)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	res, status, err := ResolveWithCache(root, javaCtx(), newEvaluator(t), CacheOptions{Disabled: true})
	if err != nil {
		t.Fatalf("ResolveWithCache: %v", err)
	}
	if status.Used {
		t.Error("disabled cache must not be used")
	}
	want := []string{"common", "common-backend", "java"}
	if got := loadedLabels(res); !equalStrings(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
}

func TestCache_SaveLoadRoundTrip(t *testing.T) {
	root := stdRulesTree(t)
	cache, _, err := BuildCache(root, fixedNow())
	if err != nil {
		t.Fatalf("BuildCache: %v", err)
	}
	path := filepath.Join(t.TempDir(), "signals.yaml")
	if err := cache.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadCache(path)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if got.Meta.SourceFingerprint != cache.Meta.SourceFingerprint {
		t.Error("fingerprint did not round-trip")
	}
	if len(got.Labels) != len(cache.Labels) {
		t.Errorf("label count %d != %d", len(got.Labels), len(cache.Labels))
	}
	if got.Labels["java"].Detect != cache.Labels["java"].Detect {
		t.Error("java detect did not round-trip")
	}
}

func TestResolveWithCache_ForceRebuild(t *testing.T) {
	root := stdRulesTree(t)
	cache, _, err := BuildCache(root, fixedNow())
	if err != nil {
		t.Fatalf("BuildCache: %v", err)
	}
	if err := cache.Save(DefaultCachePath(root)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, status, err := ResolveWithCache(root, javaCtx(), newEvaluator(t), CacheOptions{ForceRebuild: true, Now: fixedNow})
	if err != nil {
		t.Fatalf("ResolveWithCache: %v", err)
	}
	if status.Used {
		t.Error("force-rebuild must not report a cache hit")
	}
	if !status.Wrote {
		t.Error("force-rebuild should rewrite the cache")
	}
}

// TestRefreshCache_BuildsWhenAbsent proves the first refresh of a tree with no
// cache writes a fresh one.
func TestRefreshCache_BuildsWhenAbsent(t *testing.T) {
	root := stdRulesTree(t)
	status, err := RefreshCache(root, fixedNow())
	if err != nil {
		t.Fatalf("RefreshCache: %v", err)
	}
	if !status.Wrote {
		t.Errorf("expected a write for an absent cache, got %+v", status)
	}
	if status.StaleRebuilt {
		t.Error("absent cache is not a stale rebuild")
	}
	if _, err := os.Stat(DefaultCachePath(root)); err != nil {
		t.Errorf("signals.yaml should exist after refresh: %v", err)
	}
}

// TestRefreshCache_NoWriteWhenFresh proves the loop-safety invariant: a second
// refresh with no source change does not rewrite the cache. This is what stops
// the watch loop from re-firing on its own signals.yaml write.
func TestRefreshCache_NoWriteWhenFresh(t *testing.T) {
	root := stdRulesTree(t)
	if _, err := RefreshCache(root, fixedNow()); err != nil {
		t.Fatalf("first RefreshCache: %v", err)
	}
	status, err := RefreshCache(root, fixedNow())
	if err != nil {
		t.Fatalf("second RefreshCache: %v", err)
	}
	if status.Wrote {
		t.Error("a fresh cache must not be rewritten (would loop the watcher)")
	}
	if !status.Used {
		t.Errorf("expected the fresh cache to be recognised, got %+v", status)
	}
}

// TestRefreshCache_RebuildsWhenStale proves a rule edit triggers a rebuild.
func TestRefreshCache_RebuildsWhenStale(t *testing.T) {
	root := stdRulesTree(t)
	if _, err := RefreshCache(root, fixedNow()); err != nil {
		t.Fatalf("initial RefreshCache: %v", err)
	}

	// Edit a rule and bump its mtime so the fingerprint changes.
	edited := writeFile(t, root, "java/java.md", "---\nlabel: java\ndetect: \"false\"\nextends: [common-backend]\npriority: 20\n---\nEDITED")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(edited, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	status, err := RefreshCache(root, fixedNow())
	if err != nil {
		t.Fatalf("RefreshCache after edit: %v", err)
	}
	if !status.StaleRebuilt || !status.Wrote {
		t.Errorf("expected stale rebuild + write after edit, got %+v", status)
	}

	reloaded, err := LoadCache(DefaultCachePath(root))
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	fp, _ := Fingerprint(root)
	if !reloaded.fresh(fp) {
		t.Error("cache should be fresh for the new fingerprint after rebuild")
	}
}
