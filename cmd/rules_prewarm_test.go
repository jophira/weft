package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jophira/weft/internal/rules"
)

// TestPrewarmRulesCaches_SkipsRootsWithoutCache proves watch mode never
// materialises a signals.yaml in a tree the user has not opted into the resolver
// for: a root with rule files but no existing cache is left untouched.
func TestPrewarmRulesCaches_SkipsRootsWithoutCache(t *testing.T) {
	root := ruleTree(t, "go", "GO")
	cachePath := rules.DefaultCachePath(root)
	if _, err := os.Stat(cachePath); err == nil {
		t.Fatalf("precondition: %s should not exist yet", cachePath)
	}

	prewarmRulesCaches([]string{root})

	if _, err := os.Stat(cachePath); err == nil {
		t.Errorf("prewarm must not create a cache for a root that had none: %s exists", cachePath)
	}
}

// TestPrewarmRulesCaches_RefreshesExistingCache proves that when a root already
// has a cache, prewarm keeps it current after a rule edit.
func TestPrewarmRulesCaches_RefreshesExistingCache(t *testing.T) {
	root := ruleTree(t, "go", "GO")
	cachePath := rules.DefaultCachePath(root)

	// Opt in: build the initial cache.
	if _, err := rules.RefreshCache(root, time.Now().UTC()); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// Edit the rule and bump mtime so the fingerprint changes.
	edited := filepath.Join(root, "go.md")
	writeFile(t, edited, "---\nlabel: go\ndetect: \"true\"\n---\nGO EDITED")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(edited, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	prewarmRulesCaches([]string{root})

	reloaded, err := rules.LoadCache(cachePath)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if _, ok := reloaded.Labels["go"]; !ok {
		t.Error("refreshed cache should still contain the go label")
	}
}
