package cmd

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/advice"
	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/project"
)

// gitRepoT makes a real repository, because ownership asks git whether a file
// is tracked.
func gitRepoT(t *testing.T) string {
	t.Helper()
	// The developer's own global excludes would change what git status shows.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	gitT(t, root, "init", "-q")
	return root
}

func gitT(t *testing.T, root string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)
	out, err := exec.Command("git", full...).CombinedOutput() //nolint:gosec // test helper, args are literals
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// harnessesT picks harnesses by name. Detection is irrelevant to syncProject,
// which delivers to whatever it is handed.
func harnessesT(t *testing.T, names ...string) []harness.Harness {
	t.Helper()
	reg := harness.NewRegistry(harness.Instances()...)
	var out []harness.Harness
	for _, n := range names {
		h, ok := reg.Get(n)
		if !ok {
			t.Fatalf("unknown harness %q", n)
		}
		out = append(out, h)
	}
	return out
}

// allowProjectWrite opts a harness into project-write for one test.
func allowProjectWrite(t *testing.T, name string) {
	t.Helper()
	viper.Set(projectWriteKey, map[string]any{name: true})
	t.Cleanup(func() { viper.Set(projectWriteKey, map[string]any{}) })
}

func syncT(t *testing.T, root string, names ...string) []project.Delivered {
	t.Helper()
	viper.Set("active_profile", "") // no rules bundle: the repo's own files are the input
	delivered, err := syncProject(root, harnessesT(t, names...), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("syncProject: %v", err)
	}
	return delivered
}

func deliveredTo(ds []project.Delivered, name string) *project.Delivered {
	for i := range ds {
		if ds[i].Harness == name {
			return &ds[i]
		}
	}
	return nil
}

func excludeOf(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("read exclude: %v", err)
	}
	return string(data)
}

func statusOf(t *testing.T, root string) string {
	t.Helper()
	return gitT(t, root, "status", "--porcelain", "-uall")
}

func TestSyncProject_createdImportTargetIsExcluded(t *testing.T) {
	root := gitRepoT(t)
	writeFileT(t, filepath.Join(root, "CLAUDE.md"), "# Rules\n\nBe brief.\n")

	ds := syncT(t, root, "claude-code", "gemini-cli")

	if deliveredTo(ds, "gemini-cli") == nil {
		t.Fatal("gemini-cli received nothing, so this test would prove nothing")
	}
	if _, err := os.Stat(filepath.Join(root, "GEMINI.md")); err != nil {
		t.Fatalf("GEMINI.md was not created: %v", err)
	}
	if !strings.Contains(excludeOf(t, root), "/GEMINI.md") {
		t.Errorf("info/exclude = %q, want /GEMINI.md", excludeOf(t, root))
	}
	if strings.Contains(statusOf(t, root), "GEMINI.md") {
		t.Errorf("git status lists GEMINI.md:\n%s", statusOf(t, root))
	}
}

func TestSyncProject_importIntoForeignFileNeedsOptIn(t *testing.T) {
	// Ungated control first: the same inputs do reach Gemini, so the assertion
	// below cannot pass merely because there was nothing to deliver.
	control := gitRepoT(t)
	writeFileT(t, filepath.Join(control, "CLAUDE.md"), "# Rules\n\nBe brief.\n")
	if deliveredTo(syncT(t, control, "claude-code", "gemini-cli"), "gemini-cli") == nil {
		t.Fatal("control repo: gemini-cli received nothing")
	}

	root := gitRepoT(t)
	writeFileT(t, filepath.Join(root, "CLAUDE.md"), "# Rules\n\nBe brief.\n")
	theirs := "# Theirs\n\nTeam notes.\n"
	writeFileT(t, filepath.Join(root, "GEMINI.md"), theirs)

	if ds := syncT(t, root, "claude-code", "gemini-cli"); deliveredTo(ds, "gemini-cli") != nil {
		t.Error("gemini-cli was delivered to a file weft does not own, without opt-in")
	}

	// Codex waiting on inline opt-in raises its own advice first; the import
	// advice must survive it rather than being deduplicated away.
	bus := advice.New(advice.Options{})
	advice.SetDefault(bus)
	t.Cleanup(func() { advice.SetDefault(advice.New(advice.Options{})) })
	reportDelivery(io.Discard, root, nil, harnessesT(t, "claude-code", "gemini-cli", "codex"))
	var adviceOut bytes.Buffer
	bus.Emit(&adviceOut)
	if !strings.Contains(adviceOut.String(), "gemini-cli needs to edit a file weft did not create") {
		t.Errorf("no import advice for gemini-cli:\n%s", adviceOut.String())
	}
	got, _ := os.ReadFile(filepath.Join(root, "GEMINI.md"))
	if string(got) != theirs {
		t.Errorf("GEMINI.md changed without opt-in:\n%s", got)
	}

	allowProjectWrite(t, "gemini-cli")
	d := deliveredTo(syncT(t, root, "claude-code", "gemini-cli"), "gemini-cli")
	if d == nil || !d.Wrote || !d.TrackedByGit {
		t.Fatalf("with project-write on, delivery = %+v, want a write flagged as tracked", d)
	}
	got, _ = os.ReadFile(filepath.Join(root, "GEMINI.md"))
	if !strings.Contains(string(got), "Team notes.") || !strings.Contains(string(got), "@./.weft/") {
		t.Errorf("GEMINI.md = %q, want their text plus weft's import", got)
	}
}

func TestSyncProject_importIntoOwnFileKeepsWorking(t *testing.T) {
	root := gitRepoT(t)
	writeFileT(t, filepath.Join(root, "CLAUDE.md"), "# Rules\n\nBe brief.\n")
	syncT(t, root, "claude-code", "gemini-cli")

	// A second input adds an import line, so a second sync has something to write.
	writeFileT(t, filepath.Join(root, ".claude", "CLAUDE.md"), "# More\n\nAlso this.\n")
	d := deliveredTo(syncT(t, root, "claude-code", "gemini-cli"), "gemini-cli")
	if d == nil || !d.Wrote {
		t.Fatalf("second sync delivery = %+v, want a write into the file weft made", d)
	}
	if d.TrackedByGit {
		t.Error("a file weft owns was reported as tracked by git")
	}
	got, _ := os.ReadFile(filepath.Join(root, "GEMINI.md"))
	if n := strings.Count(string(got), "@./.weft/"); n != 2 {
		t.Errorf("GEMINI.md has %d import lines, want 2:\n%s", n, got)
	}
}

func TestSyncProject_removedExclusionIsNotRestored(t *testing.T) {
	root := gitRepoT(t)
	writeFileT(t, filepath.Join(root, "CLAUDE.md"), "# Rules\n\nBe brief.\n")
	syncT(t, root, "claude-code", "gemini-cli")

	excludePath := filepath.Join(root, ".git", "info", "exclude")
	kept := strings.ReplaceAll(excludeOf(t, root), "/GEMINI.md\n", "")
	if err := os.WriteFile(excludePath, []byte(kept), 0o644); err != nil {
		t.Fatalf("write exclude: %v", err)
	}

	writeFileT(t, filepath.Join(root, ".claude", "CLAUDE.md"), "# More\n\nAlso this.\n")
	if ds := syncT(t, root, "claude-code", "gemini-cli"); deliveredTo(ds, "gemini-cli") != nil {
		t.Error("the file became foreign when its exclusion was removed, so it should be gated")
	}
	if strings.Contains(excludeOf(t, root), "/GEMINI.md") {
		t.Error("sync restored an exclusion the user removed")
	}
	got, _ := os.ReadFile(filepath.Join(root, "GEMINI.md"))
	if n := strings.Count(string(got), "@./.weft/"); n != 1 {
		t.Errorf("GEMINI.md has %d import lines, want it left at 1:\n%s", n, got)
	}
}

func TestSyncProject_inlineWithoutOptInWritesNothing(t *testing.T) {
	root := gitRepoT(t)
	writeFileT(t, filepath.Join(root, "CLAUDE.md"), "# Rules\n\nBe brief.\n")

	if ds := syncT(t, root, "claude-code", "codex"); deliveredTo(ds, "codex") != nil {
		t.Error("codex was delivered to without opt-in")
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("AGENTS.md exists without opt-in (stat err = %v)", err)
	}

	allowProjectWrite(t, "codex")
	d := deliveredTo(syncT(t, root, "claude-code", "codex"), "codex")
	if d == nil || !d.Wrote || d.TrackedByGit {
		t.Fatalf("with opt-in, delivery = %+v, want a write to a file weft created", d)
	}
	if !strings.Contains(excludeOf(t, root), "/AGENTS.md") {
		t.Errorf("info/exclude = %q, want /AGENTS.md", excludeOf(t, root))
	}
}

func TestSyncProject_trackedExcludedFileIsForeign(t *testing.T) {
	root := gitRepoT(t)
	writeFileT(t, filepath.Join(root, "CLAUDE.md"), "# Rules\n\nBe brief.\n")
	writeFileT(t, filepath.Join(root, "GEMINI.md"), "# Committed\n")
	gitT(t, root, "add", "GEMINI.md")
	gitT(t, root, "commit", "-q", "-m", "add")
	if _, err := project.EnsureExcluded(root, "GEMINI.md"); err != nil {
		t.Fatalf("EnsureExcluded: %v", err)
	}

	if ds := syncT(t, root, "claude-code", "gemini-cli"); deliveredTo(ds, "gemini-cli") != nil {
		t.Error("a tracked file was treated as weft's because info/exclude lists it")
	}
	got, _ := os.ReadFile(filepath.Join(root, "GEMINI.md"))
	if string(got) != "# Committed\n" {
		t.Errorf("tracked GEMINI.md was edited:\n%s", got)
	}
}
