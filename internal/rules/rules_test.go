package rules

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// writeFile writes content to dir/rel, creating parent directories.
func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return path
}

// stdRulesTree writes a rules tree mirroring the real dev/ hierarchy shape and
// returns its root. Labels: common (always), common-backend (dependency-only),
// java, springboot, vue, react.
func stdRulesTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "common.md", "---\nlabel: common\ndetect: \"true\"\npriority: 0\n---\nCOMMON")
	writeFile(t, root, "common-backend.md", "---\nlabel: common-backend\nextends: [common]\npriority: 10\n---\nBACKEND")
	writeFile(t, root, "java/java.md", "---\nlabel: java\ndetect: \"'pom.xml' in files\"\nextends: [common-backend]\npriority: 20\n---\nJAVA")
	writeFile(t, root, "java/springboot.md", "---\nlabel: springboot\ndetect: \"deps.exists(d, d.contains('spring-boot'))\"\nextends: [java]\npriority: 30\n---\nSPRINGBOOT")
	writeFile(t, root, "vue/vue.md", "---\nlabel: vue\ndetect: \"deps.exists(d, d == 'vue')\"\nextends: [common]\npriority: 20\n---\nVUE")
	writeFile(t, root, "vue/react.md", "---\nlabel: react\ndetect: \"deps.exists(d, d == 'react')\"\nextends: [common]\npriority: 20\n---\nREACT")
	return root
}

func loadedLabels(res Resolution) []string {
	out := make([]string, 0, len(res.Loaded))
	for _, lr := range res.Loaded {
		out = append(out, lr.Label)
	}
	return out
}

func equalStrings(a, b []string) bool {
	return slices.Equal(a, b)
}

func newEvaluator(t *testing.T) Evaluator {
	t.Helper()
	ev, err := NewCELEvaluator()
	if err != nil {
		t.Fatalf("NewCELEvaluator: %v", err)
	}
	return ev
}

func TestSplitFrontMatter(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantYAML string
		wantBody string
	}{
		{"present", "---\nlabel: x\n---\nbody", "label: x", "body"},
		{"crlf", "---\r\nlabel: x\r\n---\r\nbody", "label: x\r", "body"},
		{"none", "no front matter\nhere", "", "no front matter\nhere"},
		{"unterminated", "---\nlabel: x\nbody without close", "", "---\nlabel: x\nbody without close"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotYAML, gotBody := splitFrontMatter([]byte(tt.input))
			if gotYAML != tt.wantYAML || gotBody != tt.wantBody {
				t.Errorf("splitFrontMatter() = (%q, %q), want (%q, %q)", gotYAML, gotBody, tt.wantYAML, tt.wantBody)
			}
		})
	}
}

func TestParseRule_NoLabelIgnored(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "plain.md", "# just a doc\nno front matter")
	r, err := ParseRule(p)
	if err != nil {
		t.Fatalf("ParseRule: %v", err)
	}
	if r.Label != "" {
		t.Errorf("expected empty label, got %q", r.Label)
	}
	if r.Body == "" {
		t.Error("expected body to be preserved")
	}
}

func TestBuildContext_NpmDeps(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "package.json", `{"dependencies":{"vue":"^3"},"devDependencies":{"vite":"^5"}}`)
	ctx, err := BuildContext(repo)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if !contains(ctx.Files, "package.json") {
		t.Errorf("expected package.json in files, got %v", ctx.Files)
	}
	if !contains(ctx.Deps, "vue") || !contains(ctx.Deps, "vite") {
		t.Errorf("expected vue and vite in deps, got %v", ctx.Deps)
	}
}

func TestBuildContext_MavenDeps(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "pom.xml", `<project><dependencies>
		<dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-web</artifactId></dependency>
	</dependencies></project>`)
	ctx, err := BuildContext(repo)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if !contains(ctx.Deps, "org.springframework.boot") || !contains(ctx.Deps, "spring-boot-starter-web") {
		t.Errorf("expected spring-boot coordinates in deps, got %v", ctx.Deps)
	}
}

func TestBuildContext_MalformedManifestIsResilient(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "package.json", `{ this is not valid json`)
	ctx, err := BuildContext(repo)
	if err != nil {
		t.Fatalf("BuildContext should not fail on malformed manifest: %v", err)
	}
	if len(ctx.Deps) != 0 {
		t.Errorf("expected no deps from malformed manifest, got %v", ctx.Deps)
	}
}

func TestResolve_JavaWithSpringBoot(t *testing.T) {
	root := stdRulesTree(t)
	ctx := Context{Files: []string{"pom.xml"}, Deps: []string{"org.springframework.boot", "spring-boot-starter-web"}}
	res, err := Resolve(root, ctx, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{"common", "common-backend", "java", "springboot"}
	if got := loadedLabels(res); !equalStrings(got, want) {
		t.Errorf("load order = %v, want %v", got, want)
	}
}

// TestResolve_JavaWithoutSpringBoot proves detection discriminates: a Java repo
// that does not depend on Spring Boot must not pull springboot.md.
func TestResolve_JavaWithoutSpringBoot(t *testing.T) {
	root := stdRulesTree(t)
	ctx := Context{Files: []string{"pom.xml"}, Deps: []string{"junit"}}
	res, err := Resolve(root, ctx, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{"common", "common-backend", "java"}
	if got := loadedLabels(res); !equalStrings(got, want) {
		t.Errorf("load order = %v, want %v (springboot must be absent)", got, want)
	}
}

// TestResolve_VueNotReact proves a Vue repo loads vue but not react.
func TestResolve_VueNotReact(t *testing.T) {
	root := stdRulesTree(t)
	ctx := Context{Files: []string{"package.json"}, Deps: []string{"vue"}}
	res, err := Resolve(root, ctx, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	labels := loadedLabels(res)
	if !contains(labels, "vue") {
		t.Errorf("expected vue loaded, got %v", labels)
	}
	if contains(labels, "react") {
		t.Errorf("react must not load for a vue repo, got %v", labels)
	}
}

// TestResolve_DependencyOnlyRulePulledByExtends proves common-backend (which has
// no detect predicate of its own) is loaded because java extends it.
func TestResolve_DependencyOnlyRulePulledByExtends(t *testing.T) {
	root := stdRulesTree(t)
	ctx := Context{Files: []string{"pom.xml"}, Deps: []string{"junit"}}
	res, err := Resolve(root, ctx, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, lr := range res.Loaded {
		if lr.Label == "common-backend" {
			if lr.Direct {
				t.Error("common-backend should be loaded as a dependency, not a direct match")
			}
			return
		}
	}
	t.Errorf("expected common-backend pulled via extends, got %v", loadedLabels(res))
}

// TestResolve_TreeWithNoLabelsYieldsNothing is the atomic form of the work-tech
// gap: a tree of rule-shaped files that all lack front-matter contributes
// nothing — no rule loads, and nothing errors.
func TestResolve_TreeWithNoLabelsYieldsNothing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "common.md", "# common\n\nno front-matter\n")
	writeFile(t, root, "java/java.md", "# java\n\nno front-matter\n")
	res, err := Resolve(root, Context{Files: []string{"pom.xml"}}, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Loaded) != 0 {
		t.Errorf("expected no rules loaded from an un-annotated tree, got %v", loadedLabels(res))
	}
	if res.Bundle() != "" {
		t.Errorf("expected empty bundle, got %q", res.Bundle())
	}
}

// TestResolve_MixedAnnotatedAndUnlabeled_OnlyAnnotatedLoads proves that adding
// front-matter to just some files makes exactly those participate; the rest stay
// invisible.
func TestResolve_MixedAnnotatedAndUnlabeled_OnlyAnnotatedLoads(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "common.md", "---\nlabel: common\ndetect: \"true\"\n---\nCOMMON")
	writeFile(t, root, "notes.md", "# just notes, no front-matter\n")
	writeFile(t, root, "java/java.md", "# java, forgot front-matter\n")
	res, err := Resolve(root, Context{Files: []string{"pom.xml"}}, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := loadedLabels(res); !equalStrings(got, []string{"common"}) {
		t.Errorf("load = %v, want only [common]", got)
	}
}

func TestResolve_DuplicateLabelSkipped(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\nlabel: dup\ndetect: \"true\"\n---\nA")
	writeFile(t, root, "b.md", "---\nlabel: dup\ndetect: \"true\"\n---\nB")
	res, err := Resolve(root, Context{}, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Loaded) != 1 {
		t.Errorf("expected 1 loaded rule, got %d", len(res.Loaded))
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("expected 1 skipped duplicate, got %d", len(res.Skipped))
	}
	// a.md sorts before b.md, so b.md is the skipped duplicate.
	if filepath.Base(res.Skipped[0].Path) != "b.md" {
		t.Errorf("expected b.md skipped, got %s", res.Skipped[0].Path)
	}
}

func TestResolve_UnknownExtendsRecorded(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "x.md", "---\nlabel: x\ndetect: \"true\"\nextends: [ghost]\n---\nX")
	res, err := Resolve(root, Context{}, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !contains(res.UnknownExtends, "ghost") {
		t.Errorf("expected ghost in UnknownExtends, got %v", res.UnknownExtends)
	}
}

func TestResolve_InvalidPredicateSkipped(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "bad.md", "---\nlabel: bad\ndetect: \"this is not valid CEL (((\"\n---\nBAD")
	res, err := Resolve(root, Context{}, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve should not fail on a bad predicate: %v", err)
	}
	if len(res.Loaded) != 0 {
		t.Errorf("expected no loaded rules, got %v", loadedLabels(res))
	}
	if len(res.Skipped) != 1 {
		t.Errorf("expected the bad rule to be skipped, got %d skips", len(res.Skipped))
	}
}

func TestResolve_BundleConcatenatesInOrder(t *testing.T) {
	root := stdRulesTree(t)
	ctx := Context{Files: []string{"pom.xml"}, Deps: []string{"junit"}}
	res, err := Resolve(root, ctx, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := "COMMON\n\nBACKEND\n\nJAVA"
	if got := res.Bundle(); got != want {
		t.Errorf("Bundle() = %q, want %q", got, want)
	}
}

func TestManifest_ResolutionHashStableAndSelective(t *testing.T) {
	root := stdRulesTree(t)
	now := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	javaCtx := Context{Files: []string{"pom.xml"}, Deps: []string{"junit"}}
	vueCtx := Context{Files: []string{"package.json"}, Deps: []string{"vue"}}

	res1, _ := Resolve(root, javaCtx, newEvaluator(t))
	res2, _ := Resolve(root, javaCtx, newEvaluator(t))
	resVue, _ := Resolve(root, vueCtx, newEvaluator(t))

	h1 := NewManifest(res1, root, "/repo", now).ResolutionHash
	h2 := NewManifest(res2, root, "/repo", now).ResolutionHash
	hVue := NewManifest(resVue, root, "/repo", now).ResolutionHash

	if h1 != h2 {
		t.Errorf("identical resolves must share a hash: %s vs %s", h1, h2)
	}
	if h1 == hVue {
		t.Error("different selections must produce different hashes")
	}
}

// contains reports whether s holds v. (test helper)
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
