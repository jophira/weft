package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exercise cache self-heal at the *command* level: a resolve with the
// signals.yaml cache ENABLED, followed by a front-matter edit, followed by a
// second resolve — asserting the bundle reflects the edit with no manual rebuild.
// The internal/rules unit suite proves this for a single tree; here it is driven
// through resolveRootSpecs → resolveAcrossRoots → ResolveWithCache off the active
// profile, the same path a SessionStart hook takes. The cache is an optimization
// only: it must change speed, never the result.

// resolveBundleCached runs `weft rules resolve <repo>` with the cache enabled
// (unlike resolveBundle, which disables it) and the work-plane KB off. Global
// resolve flags are saved and restored.
func resolveBundleCached(t *testing.T, repo string) string {
	t.Helper()
	savedRoot, savedNoCache, savedNoWork := rulesRoot, rulesNoCache, rulesNoWork
	savedRecord, savedManifest, savedRebuild := rulesRecord, rulesShowManife, rulesRebuild
	t.Cleanup(func() {
		rulesRoot, rulesNoCache, rulesNoWork = savedRoot, savedNoCache, savedNoWork
		rulesRecord, rulesShowManife, rulesRebuild = savedRecord, savedManifest, savedRebuild
	})
	rulesRoot, rulesNoCache, rulesNoWork = "", false, true
	rulesRecord, rulesShowManife, rulesRebuild = false, false, false
	return runCmd(t, rulesResolveCmd, []string{repo})
}

// srcRulePath is the on-disk path of a rule file inside a source added by
// addSource (which roots each source at base/srcs/<name>).
func srcRulePath(base, name, rel string) string {
	return filepath.Join(base, "srcs", name, rel)
}

// editRule rewrites a rule file and bumps its mtime into the future, so the
// stat-fingerprint changes even when the new content is the same size as the old
// — forcing the cache to notice the edit.
func editRule(t *testing.T, path, content string) {
	t.Helper()
	writeFileT(t, path, content)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// TestResolveCache_BaselineWritesSignals proves the first cached resolve of a
// tree with no cache writes a signals.yaml beside the rules — the fast path other
// self-heal tests build on.
func TestResolveCache_BaselineWritesSignals(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "s", 10, map[string]string{
		"common.md": rule("common", "true", "COMMON_BODY"),
	})
	createProfile(t, "p", "s")
	activate(t, "p")

	bundle := resolveBundleCached(t, repoWith(t, "README.md"))
	if !strings.Contains(bundle, "COMMON_BODY") {
		t.Errorf("baseline resolve missing rule body:\n%s", bundle)
	}
	if _, err := os.Stat(srcRulePath(base, "s", "signals.yaml")); err != nil {
		t.Errorf("expected signals.yaml written after cached resolve: %v", err)
	}
}

// TestResolveCache_SelfHealsLabelAdded proves annotating a previously un-labeled
// file after the cache was built makes it contribute on the next resolve.
func TestResolveCache_SelfHealsLabelAdded(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "s", 10, map[string]string{
		"keep.md":  rule("keep", "true", "KEEP_BODY"),
		"extra.md": "# not yet a rule\n\nEXTRA_BODY\n",
	})
	createProfile(t, "p", "s")
	activate(t, "p")
	repo := repoWith(t, "README.md")

	before := resolveBundleCached(t, repo)
	if !strings.Contains(before, "KEEP_BODY") || strings.Contains(before, "EXTRA_BODY") {
		t.Fatalf("baseline should have KEEP_BODY only:\n%s", before)
	}

	editRule(t, srcRulePath(base, "s", "extra.md"), rule("extra", "true", "EXTRA_BODY"))

	after := resolveBundleCached(t, repo)
	if !strings.Contains(after, "EXTRA_BODY") {
		t.Errorf("newly-labeled file must contribute after self-heal:\n%s", after)
	}
}

// TestResolveCache_SelfHealsLabelRemoved proves stripping front-matter after the
// cache was built removes the file's contribution on the next resolve.
func TestResolveCache_SelfHealsLabelRemoved(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "s", 10, map[string]string{
		"keep.md": rule("keep", "true", "KEEP_BODY"),
		"drop.md": rule("drop", "true", "DROP_BODY"),
	})
	createProfile(t, "p", "s")
	activate(t, "p")
	repo := repoWith(t, "README.md")

	before := resolveBundleCached(t, repo)
	if !strings.Contains(before, "DROP_BODY") {
		t.Fatalf("baseline should include DROP_BODY:\n%s", before)
	}

	editRule(t, srcRulePath(base, "s", "drop.md"), "# de-annotated\n\nDROP_BODY\n")

	after := resolveBundleCached(t, repo)
	if strings.Contains(after, "DROP_BODY") {
		t.Errorf("de-annotated file must stop contributing after self-heal:\n%s", after)
	}
	if !strings.Contains(after, "KEEP_BODY") {
		t.Errorf("the still-labeled sibling must remain:\n%s", after)
	}
}

// TestResolveCache_SelfHealsDetectChanged proves editing a rule's detect
// predicate after the cache was built flips its contribution for the same repo.
func TestResolveCache_SelfHealsDetectChanged(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "s", 10, map[string]string{
		"java.md": rule("java", "'pom.xml' in files", "JAVA_BODY"),
	})
	createProfile(t, "p", "s")
	activate(t, "p")
	repo := repoWith(t, "pom.xml") // matches the original detect

	before := resolveBundleCached(t, repo)
	if !strings.Contains(before, "JAVA_BODY") {
		t.Fatalf("baseline should select java for a pom.xml repo:\n%s", before)
	}

	// Repoint detect at a signal the repo does NOT have.
	editRule(t, srcRulePath(base, "s", "java.md"), rule("java", "'go.mod' in files", "JAVA_BODY"))

	after := resolveBundleCached(t, repo)
	if strings.Contains(after, "JAVA_BODY") {
		t.Errorf("edited detect (go.mod) must not match a pom.xml repo after self-heal:\n%s", after)
	}
}

// TestResolveCache_SelfHealsExtendsChanged proves repointing an extends target
// after the cache was built pulls in the new dependency and drops the old one.
// The base rules detect "false", so they load only via extends.
func TestResolveCache_SelfHealsExtendsChanged(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "s", 10, map[string]string{
		"a.md":    rule("a", "false", "BODY_A"),
		"b.md":    rule("b", "false", "BODY_B"),
		"leaf.md": rule("leaf", "'pom.xml' in files", "LEAF_BODY", "a"),
	})
	createProfile(t, "p", "s")
	activate(t, "p")
	repo := repoWith(t, "pom.xml")

	before := resolveBundleCached(t, repo)
	if !strings.Contains(before, "BODY_A") || strings.Contains(before, "BODY_B") {
		t.Fatalf("baseline leaf should pull in a (not b):\n%s", before)
	}

	editRule(t, srcRulePath(base, "s", "leaf.md"), rule("leaf", "'pom.xml' in files", "LEAF_BODY", "b"))

	after := resolveBundleCached(t, repo)
	if !strings.Contains(after, "BODY_B") {
		t.Errorf("repointed extends must pull in b after self-heal:\n%s", after)
	}
	if strings.Contains(after, "BODY_A") {
		t.Errorf("old extends target a must drop out after self-heal:\n%s", after)
	}
}

// TestResolveCache_SelfHealsFileDeleted proves deleting a rule file after the
// cache was built removes it from the next resolve.
func TestResolveCache_SelfHealsFileDeleted(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "s", 10, map[string]string{
		"keep.md": rule("keep", "true", "KEEP_BODY"),
		"gone.md": rule("gone", "true", "GONE_BODY"),
	})
	createProfile(t, "p", "s")
	activate(t, "p")
	repo := repoWith(t, "README.md")

	before := resolveBundleCached(t, repo)
	if !strings.Contains(before, "GONE_BODY") {
		t.Fatalf("baseline should include GONE_BODY:\n%s", before)
	}

	if err := os.Remove(srcRulePath(base, "s", "gone.md")); err != nil {
		t.Fatalf("remove gone.md: %v", err)
	}

	after := resolveBundleCached(t, repo)
	if strings.Contains(after, "GONE_BODY") {
		t.Errorf("deleted rule must not appear after self-heal:\n%s", after)
	}
	if !strings.Contains(after, "KEEP_BODY") {
		t.Errorf("surviving rule must remain:\n%s", after)
	}
}
