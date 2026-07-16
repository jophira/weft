package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/testutil"
)

// These tests exercise the composition that unit tests missed: a *profile*
// resolving across *multiple real-shaped sources* against a *repo fixture*.
// They use the in-process isolated-config scaffolding (withIsolatedConfig →
// registry + profile managers backed by a temp config), so they drive the same
// code path a session hook does: resolveRootSpecs → resolveAcrossRoots →
// layerBundles, keyed off the active profile.

// ── fixtures ─────────────────────────────────────────────────────────────────

// addSource registers a source rooted under base/srcs/<name> with the given
// rel-path → content files and priority.
func addSource(t *testing.T, base, name string, priority int, files map[string]string) {
	t.Helper()
	root := filepath.Join(base, "srcs", name)
	for rel, content := range files {
		writeFileT(t, filepath.Join(root, rel), content)
	}
	reg, err := newRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if err := reg.Add(source.Source{Name: name, Root: root, Priority: priority, Structure: source.DefaultStructure()}); err != nil {
		t.Fatalf("add source %q: %v", name, err)
	}
}

// createProfile registers a cascade profile spanning sources (does not activate).
func createProfile(t *testing.T, name string, sources ...string) {
	t.Helper()
	pm, err := newProfileManager()
	if err != nil {
		t.Fatalf("profile manager: %v", err)
	}
	if err := pm.Create(profile.Profile{Name: name, Sources: sources, Overlay: profile.OverlayCascade}); err != nil {
		t.Fatalf("create profile %q: %v", name, err)
	}
}

// activate points the active profile at name for the current test.
func activate(t *testing.T, name string) {
	t.Helper()
	viper.Set("active_profile", name)
}

// repoWith creates a repo fixture whose root contains the named (empty) files.
func repoWith(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		writeFileT(t, filepath.Join(dir, f), "")
	}
	return dir
}

// resolveBundle runs `weft rules resolve <repo>` against the active profile and
// returns the assembled bundle, with caching and work-plane KB disabled for
// determinism. Global resolve flags are saved and restored.
func resolveBundle(t *testing.T, repo string) string {
	t.Helper()
	savedRoot, savedNoCache, savedNoWork := rulesRoot, rulesNoCache, rulesNoWork
	savedRecord, savedManifest := rulesRecord, rulesShowManife
	t.Cleanup(func() {
		rulesRoot, rulesNoCache, rulesNoWork = savedRoot, savedNoCache, savedNoWork
		rulesRecord, rulesShowManife = savedRecord, savedManifest
	})
	rulesRoot, rulesNoCache, rulesNoWork, rulesRecord, rulesShowManife = "", true, true, false, false
	return runCmd(t, rulesResolveCmd, []string{repo})
}

// A two-source world: pers (priority 10) and work (priority 20), each with an
// always-on common rule and a java rule that extends it. Labels intentionally
// repeat across sources to prove per-source label scoping — the bodies differ so
// the bundle is unambiguous.
func twoSourceWorld(t *testing.T, base string) {
	addSource(t, base, "pers", 10, map[string]string{
		"common.md": testutil.RuleFile("common", "true", "PERS_COMMON"),
		"java.md":   testutil.RuleFile("java", "'pom.xml' in files", "PERS_JAVA", "common"),
	})
	addSource(t, base, "work", 20, map[string]string{
		"common.md": testutil.RuleFile("common", "true", "WORK_COMMON"),
		"java.md":   testutil.RuleFile("java", "'pom.xml' in files", "WORK_JAVA", "common"),
	})
}

// ── happy paths ──────────────────────────────────────────────────────────────

func TestResolveIntegration_JavaRepo_BothSourcesContributeInPriorityOrder(t *testing.T) {
	base := withIsolatedConfig(t)
	twoSourceWorld(t, base)
	createProfile(t, "hybrid", "pers", "work")
	activate(t, "hybrid")

	bundle := resolveBundle(t, repoWith(t, "pom.xml"))

	for _, want := range []string{"PERS_COMMON", "PERS_JAVA", "WORK_COMMON", "WORK_JAVA"} {
		if !strings.Contains(bundle, want) {
			t.Errorf("java repo bundle missing %q:\n%s", want, bundle)
		}
	}
	// Lower-priority source (pers=10) is layered before the higher (work=20);
	// within a source, the extends dependency (common) precedes java.
	assertOrder(t, bundle, "PERS_COMMON", "PERS_JAVA", "WORK_COMMON", "WORK_JAVA")
}

func TestResolveIntegration_PlainRepo_OnlyAlwaysOnRules(t *testing.T) {
	base := withIsolatedConfig(t)
	twoSourceWorld(t, base)
	createProfile(t, "hybrid", "pers", "work")
	activate(t, "hybrid")

	bundle := resolveBundle(t, repoWith(t, "README.md"))

	for _, want := range []string{"PERS_COMMON", "WORK_COMMON"} {
		if !strings.Contains(bundle, want) {
			t.Errorf("plain repo bundle missing always-on %q:\n%s", want, bundle)
		}
	}
	for _, absent := range []string{"PERS_JAVA", "WORK_JAVA"} {
		if strings.Contains(bundle, absent) {
			t.Errorf("plain repo bundle unexpectedly contains java rule %q:\n%s", absent, bundle)
		}
	}
}

func TestResolveIntegration_ProfileSwitchChangesBundle(t *testing.T) {
	base := withIsolatedConfig(t)
	twoSourceWorld(t, base)
	createProfile(t, "hybrid", "pers", "work")
	createProfile(t, "onlypers", "pers")
	createProfile(t, "onlywork", "work")
	repo := repoWith(t, "pom.xml")

	activate(t, "onlypers")
	persBundle := resolveBundle(t, repo)
	if !strings.Contains(persBundle, "PERS_JAVA") || strings.Contains(persBundle, "WORK_JAVA") {
		t.Errorf("onlypers should contain PERS_JAVA and not WORK_JAVA:\n%s", persBundle)
	}

	activate(t, "onlywork")
	workBundle := resolveBundle(t, repo)
	if !strings.Contains(workBundle, "WORK_JAVA") || strings.Contains(workBundle, "PERS_JAVA") {
		t.Errorf("onlywork should contain WORK_JAVA and not PERS_JAVA:\n%s", workBundle)
	}

	activate(t, "hybrid")
	hybridBundle := resolveBundle(t, repo)
	if !strings.Contains(hybridBundle, "PERS_JAVA") || !strings.Contains(hybridBundle, "WORK_JAVA") {
		t.Errorf("hybrid should contain both java rules:\n%s", hybridBundle)
	}
}

// ── unhappy paths ────────────────────────────────────────────────────────────

// TestResolveIntegration_UnannotatedSourceContributesNothing is the direct
// regression guard for the work-tech gap: a source whose files carry no
// front-matter contributes nothing to a multi-source resolve, silently.
func TestResolveIntegration_UnannotatedSourceContributesNothing(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "annotated", 10, map[string]string{
		"common.md": testutil.RuleFile("common", "true", "ANNOTATED_COMMON"),
		"java.md":   testutil.RuleFile("java", "'pom.xml' in files", "ANNOTATED_JAVA", "common"),
	})
	// 'raw' looks like a rules source but has NO front-matter — exactly work-tech
	// before annotation.
	addSource(t, base, "raw", 20, map[string]string{
		"common.md":     "# Raw common\n\nRAW_COMMON no front-matter\n",
		"java/rules.md": "# Raw java\n\nRAW_JAVA no front-matter\n",
	})
	createProfile(t, "mix", "annotated", "raw")
	activate(t, "mix")

	bundle := resolveBundle(t, repoWith(t, "pom.xml"))

	if !strings.Contains(bundle, "ANNOTATED_JAVA") || !strings.Contains(bundle, "ANNOTATED_COMMON") {
		t.Errorf("annotated source should contribute its rules:\n%s", bundle)
	}
	for _, leaked := range []string{"RAW_COMMON", "RAW_JAVA"} {
		if strings.Contains(bundle, leaked) {
			t.Errorf("un-annotated source must contribute nothing, but bundle has %q:\n%s", leaked, bundle)
		}
	}
}

// TestResolveIntegration_MissingProfileSourceIsSkipped proves a profile naming
// an unregistered source does not abort the resolve — the registered sources
// still contribute.
func TestResolveIntegration_MissingProfileSourceIsSkipped(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "pers", 10, map[string]string{
		"common.md": testutil.RuleFile("common", "true", "PERS_COMMON"),
	})
	createProfile(t, "withghost", "pers", "ghost") // ghost never registered
	activate(t, "withghost")

	bundle := resolveBundle(t, repoWith(t, "README.md"))
	if !strings.Contains(bundle, "PERS_COMMON") {
		t.Errorf("resolve should skip the missing source and still emit pers:\n%s", bundle)
	}
}

// TestResolveIntegration_DuplicateLabelWithinSource_FirstWins proves the
// resolver keeps the first file (by sorted path) for a label and the bundle
// carries only its body.
func TestResolveIntegration_DuplicateLabelWithinSource_FirstWins(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "dup", 10, map[string]string{
		"a-first.md":  testutil.RuleFile("shared", "true", "FIRST_BODY"),
		"b-second.md": testutil.RuleFile("shared", "true", "SECOND_BODY"),
	})
	createProfile(t, "p", "dup")
	activate(t, "p")

	bundle := resolveBundle(t, repoWith(t, "README.md"))
	if !strings.Contains(bundle, "FIRST_BODY") {
		t.Errorf("first file (a-first.md) should win the duplicate label:\n%s", bundle)
	}
	if strings.Contains(bundle, "SECOND_BODY") {
		t.Errorf("second duplicate must be ignored, but bundle has SECOND_BODY:\n%s", bundle)
	}
}

// TestResolveIntegration_DanglingExtendsStillLoadsRule proves an extends target
// that names no rule is best-effort: the extending rule still loads.
func TestResolveIntegration_DanglingExtendsStillLoadsRule(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "s", 10, map[string]string{
		"java.md": testutil.RuleFile("java", "'pom.xml' in files", "JAVA_BODY", "nonexistent-base"),
	})
	createProfile(t, "p", "s")
	activate(t, "p")

	bundle := resolveBundle(t, repoWith(t, "pom.xml"))
	if !strings.Contains(bundle, "JAVA_BODY") {
		t.Errorf("rule with a dangling extends should still load:\n%s", bundle)
	}
}

// TestDoctorIntegration_FlagsUnannotatedSourceInProfile proves the doctor
// rule-annotation check reads the real registry + active profile and names the
// un-annotated source that contributes nothing.
func TestDoctorIntegration_FlagsUnannotatedSourceInProfile(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "annotated", 10, map[string]string{
		"common.md": testutil.RuleFile("common", "true", "OK"),
	})
	addSource(t, base, "raw", 20, map[string]string{
		"java/springboot.md": "# no front-matter\n",
	})
	createProfile(t, "mix", "annotated", "raw")
	activate(t, "mix")

	var sb strings.Builder
	reportRuleHealth(&sb)
	got := sb.String()

	if !strings.Contains(got, "Rule annotations") {
		t.Fatalf("expected a Rule annotations section:\n%s", got)
	}
	if !strings.Contains(got, `source "raw"`) || !strings.Contains(got, "contributes nothing") {
		t.Errorf("doctor should flag raw as contributing nothing:\n%s", got)
	}
	if !strings.Contains(got, "java/springboot.md") {
		t.Errorf("doctor should name the un-annotated file with a suggestion:\n%s", got)
	}
	// The fully-annotated source must not be dragged into the report as broken.
	if strings.Contains(got, `source "annotated"`) {
		t.Errorf("healthy source should not appear in the report:\n%s", got)
	}
}

// TestDoctorIntegration_AuditsSourceOutsideActiveProfile proves the rule-health
// check audits every registered source — including one that is NOT in the active
// profile — and only tags the active ones. A registered-but-inactive source that
// contributes nothing must still be surfaced (it may be the profile the user is
// about to switch to), just without the "(active profile)" marker.
func TestDoctorIntegration_AuditsSourceOutsideActiveProfile(t *testing.T) {
	base := withIsolatedConfig(t)
	addSource(t, base, "annotated", 10, map[string]string{
		"common.md": testutil.RuleFile("common", "true", "OK"),
	})
	// 'raw' is registered but left out of the active profile, and un-annotated.
	addSource(t, base, "raw", 20, map[string]string{
		"java/springboot.md": "# no front-matter\n",
	})
	createProfile(t, "onlyannotated", "annotated")
	activate(t, "onlyannotated")

	var sb strings.Builder
	reportRuleHealth(&sb)
	got := sb.String()

	if !strings.Contains(got, `source "raw"`) {
		t.Errorf("a registered-but-inactive source should still be audited:\n%s", got)
	}
	if strings.Contains(got, `source "raw" (active profile)`) {
		t.Errorf("an inactive source must not be tagged as active:\n%s", got)
	}
	if !strings.Contains(got, "contributes nothing") {
		t.Errorf("the inactive un-annotated source should be flagged as contributing nothing:\n%s", got)
	}
	// The healthy active source must not be dragged into the report.
	if strings.Contains(got, `source "annotated"`) {
		t.Errorf("healthy active source should not appear in the report:\n%s", got)
	}
}

// assertOrder fails unless each needle appears in the given left-to-right order.
func assertOrder(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	prev := -1
	for _, n := range needles {
		i := strings.Index(haystack, n)
		if i < 0 {
			t.Errorf("missing %q in:\n%s", n, haystack)
			return
		}
		if i <= prev {
			t.Errorf("%q out of order in:\n%s", n, haystack)
			return
		}
		prev = i
	}
}
