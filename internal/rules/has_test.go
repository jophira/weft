package rules

import (
	"path/filepath"
	"testing"
)

// evalHas is a small helper: build a repo Context rooted at repo and evaluate a
// single hasFile()-based predicate against it.
func evalHas(t *testing.T, repo, predicate string) bool {
	t.Helper()
	ctx, err := BuildContext(repo)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	matched, err := newEvaluator(t).Eval(predicate, ctx)
	if err != nil {
		t.Fatalf("Eval(%q): %v", predicate, err)
	}
	return matched
}

// TestHas_NestedManifest proves hasFile() sees a manifest nested below the root,
// which the root-only `files` variable cannot.
func TestHas_NestedManifest(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "services/api/pom.xml", "<project/>")

	if !evalHas(t, repo, `hasFile("**/pom.xml")`) {
		t.Error(`hasFile("**/pom.xml") should match services/api/pom.xml`)
	}
	if !evalHas(t, repo, `hasFile("services/*/pom.xml")`) {
		t.Error(`hasFile("services/*/pom.xml") should match services/api/pom.xml`)
	}
}

// TestHas_RootManifest proves the doublestar `**/` prefix also matches a file at
// the repo root, so hasFile("**/go.mod") is a superset of `"go.mod" in files`.
func TestHas_RootManifest(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "go.mod", "module x")

	if !evalHas(t, repo, `hasFile("**/go.mod")`) {
		t.Error(`hasFile("**/go.mod") should match a root-level go.mod`)
	}
}

// TestHas_NoMatch proves hasFile() returns false when nothing matches, rather than
// erroring.
func TestHas_NoMatch(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "go.mod", "module x")

	if evalHas(t, repo, `hasFile("**/Cargo.toml")`) {
		t.Error(`hasFile("**/Cargo.toml") should not match a Go repo`)
	}
}

// TestHas_SkipsHiddenDirs proves files inside hidden directories (.git, .idea)
// are invisible to hasFile(), consistent with loadTree's tree walk.
func TestHas_SkipsHiddenDirs(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".git/modules/sub/pom.xml", "<project/>")
	writeFile(t, repo, "README.md", "# repo")

	if evalHas(t, repo, `hasFile("**/pom.xml")`) {
		t.Error("hasFile() must not see files inside hidden directories")
	}
}

// TestHas_EmptyPatternAndRoot proves the total-function contract: an empty
// pattern, or a Context without a Root, matches nothing instead of erroring.
func TestHas_EmptyPatternAndRoot(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "go.mod", "module x")

	if evalHas(t, repo, `hasFile("")`) {
		t.Error(`hasFile("") should match nothing`)
	}

	// A Context with no Root (as some callers construct directly) must not panic
	// or match.
	matched, err := newEvaluator(t).Eval(`hasFile("**/go.mod")`, Context{})
	if err != nil {
		t.Fatalf("Eval with empty Root: %v", err)
	}
	if matched {
		t.Error("hasFile() with empty Root should match nothing")
	}
}

// TestHas_CachedWalkIsStable proves repeated hasFile() calls against the same root
// (the common multi-rule case) are consistent — the memoised walk returns the
// same answer every time.
func TestHas_CachedWalkIsStable(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, filepath.Join("apps", "web", "package.json"), `{}`)
	ev := newEvaluator(t)
	ctx, err := BuildContext(repo)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	for i := range 3 {
		matched, err := ev.Eval(`hasFile("apps/**/package.json")`, ctx)
		if err != nil {
			t.Fatalf("Eval iteration %d: %v", i, err)
		}
		if !matched {
			t.Errorf("iteration %d: expected match for nested package.json", i)
		}
	}
}

// TestResolve_HasDrivenRule proves a rule whose detect uses hasFile() participates
// in a full resolve, pulling its extends chain like any other match.
func TestResolve_HasDrivenRule(t *testing.T) {
	rules := t.TempDir()
	writeFile(t, rules, "common.md", "---\nlabel: common\ndetect: \"true\"\npriority: 0\n---\nCOMMON")
	writeFile(t, rules, "go.md", "---\nlabel: go\ndetect: 'hasFile(\"**/go.mod\")'\nextends: [common]\npriority: 20\n---\nGO")

	repo := t.TempDir()
	writeFile(t, repo, "services/worker/go.mod", "module worker")

	ctx, err := BuildContext(repo)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	res, err := Resolve(rules, ctx, newEvaluator(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{"common", "go"}
	if got := loadedLabels(res); !equalStrings(got, want) {
		t.Errorf("load order = %v, want %v", got, want)
	}
}
