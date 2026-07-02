//go:build e2e

// Package e2e drives the real weft binary as a black box: it builds the CLI
// once, then execs it against a fully isolated $HOME so every artefact weft
// touches (config, sources registry, profiles, manifests, staged output and the
// harness files under ~/.claude and ~/.codex) lands inside a throwaway temp
// tree. Nothing on the developer's machine is read or written, so "revert to the
// earlier state" is simply the temp dir being removed at the end of the test.
//
// These are intentionally tagged `e2e` (mirroring the Docker-backed
// `integration` tests) because they build a binary and shell out — the fast,
// in-process projection/write-back e2e tests in cmd/ stay untagged. Run with:
//
//	make e2e            # or: go test -tags e2e ./test/e2e/...
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// weftBin is the absolute path to the CLI built once in TestMain.
var weftBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "weft-e2e-bin-*")
	if err != nil {
		panic("creating temp bin dir: " + err.Error())
	}
	defer os.RemoveAll(dir)

	weftBin = filepath.Join(dir, "weft")
	if runtime.GOOS == "windows" {
		weftBin += ".exe"
	}

	// The test package lives at test/e2e; the module root is two levels up.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic("resolving module root: " + err.Error())
	}
	build := exec.Command("go", "build", "-o", weftBin, ".")
	build.Dir = root
	if out, buildErr := build.CombinedOutput(); buildErr != nil {
		panic("building weft binary: " + buildErr.Error() + "\n" + string(out))
	}

	os.Exit(m.Run())
}

// runWeft execs the built binary with a hermetic environment rooted at home.
// It fails the test on a non-zero exit and returns the combined output so the
// caller can assert on what the user would see.
func runWeft(t *testing.T, home string, args ...string) string {
	t.Helper()
	cmd := exec.Command(weftBin, args...)
	cmd.Env = hermeticEnv(home)
	cmd.Stdin = strings.NewReader("") // never block on a prompt
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("weft %s\n  exit: %v\n  output:\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// hermeticEnv strips any inherited HOME/USERPROFILE/CI from the parent process
// and pins them to the isolated home. CI=1 disables the background update check
// and git auto-sync so the run never touches the network or a real TTY.
func hermeticEnv(home string) []string {
	drop := map[string]bool{"HOME": true, "USERPROFILE": true, "CI": true}
	var env []string
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && drop[k] {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+home,
		"USERPROFILE="+home, // Windows home resolution
		"CI=1",
	)
}

// writeFile creates path (and parents) with content, failing the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func mustContain(t *testing.T, label, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s: expected to contain %q\n---\n%s\n---", label, needle, haystack)
	}
}

func mustNotContain(t *testing.T, label, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("%s: expected NOT to contain %q\n---\n%s\n---", label, needle, haystack)
	}
}

// mustOrder asserts each needle appears, in the given left-to-right order.
func mustOrder(t *testing.T, label, haystack string, needles ...string) {
	t.Helper()
	prev := -1
	for _, n := range needles {
		i := strings.Index(haystack, n)
		if i < 0 {
			t.Errorf("%s: missing %q\n---\n%s\n---", label, n, haystack)
			return
		}
		if i <= prev {
			t.Errorf("%s: %q out of order (index %d ≤ previous %d)\n---\n%s\n---", label, n, i, prev, haystack)
			return
		}
		prev = i
	}
}

// TestCLIEndToEnd walks the full lifecycle through the real binary:
//
//	3 sources (personal / work / work-private) → profile create → profile use
//	→ assert projection into Claude (Tier A) and Codex (Tier B) harness files
//	→ external edit in the Codex file → re-apply → assert write-back to source
//	→ tear the profile/sources down and restore the pre-existing harness files.
func TestCLIEndToEnd(t *testing.T) {
	// Isolation: weft resolves every path (~/.claude, ~/.codex, ~/.config/weft,
	// manifests, backups, staged) from $HOME. hermeticEnv() pins HOME/USERPROFILE
	// to this temp dir, so the harness files below are dummies inside a throwaway
	// tree — the developer's real ~/.claude / ~/.codex are never read or written.
	// t.TempDir() removes the whole tree when the test ends, on pass AND on
	// failure (t.Fatal unwinds via runtime.Goexit, which runs cleanups), so state
	// is always left clean regardless of outcome.
	home := t.TempDir()

	// Guardrail: fail loudly rather than risk real files if the env override ever
	// regresses and weft falls back to the developer's actual home directory.
	if realHome, err := os.UserHomeDir(); err == nil && (home == realHome || strings.HasPrefix(realHome, home+string(os.PathSeparator))) {
		t.Fatalf("test $HOME %q is not isolated from the real home %q — aborting to avoid touching real harness files", home, realHome)
	}

	// ── 1. Three dummy sources with distinct folder structures ────────────────
	srcRoot := t.TempDir()
	personal := filepath.Join(srcRoot, "personal")
	work := filepath.Join(srcRoot, "work")
	workPrivate := filepath.Join(srcRoot, "work-private")

	// personal: flat CLAUDE.md with a projects placeholder, a project-rules tree,
	// and a commands/ sidecar.
	writeFile(t, filepath.Join(personal, "CLAUDE.md"), "# personal rules\n\n<!-- weft:projects -->\n")
	writeFile(t, filepath.Join(personal, "project-rules", "myproj", "myproj.md"), "# myproj project rules")
	writeFile(t, filepath.Join(personal, "commands", "hello.md"), "say hello")

	// work: flat CLAUDE.md plus a skills/ sidecar.
	writeFile(t, filepath.Join(work, "CLAUDE.md"), "# work rules")
	writeFile(t, filepath.Join(work, "skills", "lint", "SKILL.md"), "# lint skill")

	// work-private: a domain hierarchy assembled via instruction_glob.
	writeFile(t, filepath.Join(workPrivate, "CLAUDE.md"), "# work-private rules")
	writeFile(t, filepath.Join(workPrivate, "Backend", "BACKEND.md"), "# backend domain rules")

	// ── 2. Pre-existing harness files the user owns (the "before" state) ───────
	claudePath := filepath.Join(home, ".claude", "CLAUDE.md")
	codexPath := filepath.Join(home, ".codex", "AGENTS.md")
	claudeBefore := "# MY OWN GLOBAL NOTES\nkeep this forever\n"
	codexBefore := "# MY OWN CODEX NOTES\nkeep this too\n"
	writeFile(t, claudePath, claudeBefore)
	writeFile(t, codexPath, codexBefore)

	// Deterministic teardown: registered now so it runs on success, on t.Fatal,
	// and after any assertion failure — restoring the harness files to the "before"
	// state and deregistering everything weft created. (t.TempDir() would discard
	// the tree anyway; this proves the revert cycle runs regardless of outcome.)
	t.Cleanup(func() {
		for _, args := range [][]string{
			{"profile", "delete", "hybrid"},
			{"source", "remove", "personal"},
			{"source", "remove", "work"},
			{"source", "remove", "work-private"},
		} {
			cmd := exec.Command(weftBin, args...)
			cmd.Env = hermeticEnv(home) // stay isolated even during teardown
			cmd.Stdin = strings.NewReader("")
			_ = cmd.Run() // best-effort: absent profile/source is fine
		}
		writeFile(t, claudePath, claudeBefore)
		writeFile(t, codexPath, codexBefore)
		if got := readFile(t, claudePath); got != claudeBefore {
			t.Errorf("teardown: claude harness not reverted:\n%s", got)
		}
		if got := readFile(t, codexPath); got != codexBefore {
			t.Errorf("teardown: codex harness not reverted:\n%s", got)
		}
	})

	// ── 3. Register the sources (lowest → highest priority) ────────────────────
	runWeft(t, home, "source", "add", "personal", personal, "--priority", "10")
	runWeft(t, home, "source", "add", "work", work, "--priority", "20")
	runWeft(t, home, "source", "add", "work-private", workPrivate, "--priority", "30", "--instruction-glob", "**/*.md")

	list := runWeft(t, home, "source", "list")
	for _, name := range []string{"personal", "work", "work-private"} {
		mustContain(t, "source list", list, name)
	}

	// ── 4. Create and activate a profile targeting both harnesses ──────────────
	runWeft(t, home, "profile", "create", "hybrid",
		"--sources", "personal,work,work-private",
		"--target", "claude-code", "--target", "codex")
	runWeft(t, home, "profile", "use", "hybrid", "--no-watch")

	// ── 5. Assert the "after" state ────────────────────────────────────────────

	// Tier A (Claude Code): a thin import block in priority order, user content
	// preserved, no inlined source bodies.
	claude := readFile(t, claudePath)
	mustContain(t, "claude", claude, "weft:begin")
	mustContain(t, "claude preserves user content", claude, "# MY OWN GLOBAL NOTES")
	mustContain(t, "claude preserves user content", claude, "keep this forever")
	mustOrder(t, "claude import order", claude, "00-personal.md", "01-work.md", "02-work-private.md")
	mustNotContain(t, "claude has no inlined bodies", claude, "# work rules")

	// Tier B (Codex): inlined, attributed source bodies in priority order,
	// including the glob-assembled work-private hierarchy; user content preserved.
	codex := readFile(t, codexPath)
	mustContain(t, "codex", codex, "weft:begin")
	mustContain(t, "codex preserves user content", codex, "# MY OWN CODEX NOTES")
	mustOrder(t, "codex body order", codex, "# personal rules", "# work rules", "# work-private rules")
	mustContain(t, "codex assembles work-private hierarchy", codex, "# backend domain rules")

	// weft-owned per-source instruction copies, priority-numbered.
	instrDir := filepath.Join(home, ".config", "weft", "profiles", "hybrid", "instructions")
	for _, name := range []string{"00-personal.md", "01-work.md", "02-work-private.md"} {
		if _, err := os.Stat(filepath.Join(instrDir, name)); err != nil {
			t.Errorf("expected instruction copy %s: %v", name, err)
		}
	}
	personalCopy := readFile(t, filepath.Join(instrDir, "00-personal.md"))
	mustContain(t, "personal copy expands projects placeholder", personalCopy, "weft:projects:begin")
	mustContain(t, "personal copy lists project file", personalCopy, "myproj.md")

	// Sidecars copied into the Claude harness dir.
	for _, rel := range []string{
		filepath.Join("commands", "hello.md"),
		filepath.Join("skills", "lint", "SKILL.md"),
	} {
		if _, err := os.Stat(filepath.Join(home, ".claude", rel)); err != nil {
			t.Errorf("expected sidecar %s copied to ~/.claude: %v", rel, err)
		}
	}

	// ── 6. External edit in the Codex file must flow back to the source ────────
	edited := strings.Replace(codex, "# personal rules", "# EDITED personal rules via codex", 1)
	if !strings.Contains(edited, "# EDITED personal rules via codex") {
		t.Fatal("test setup: edit did not apply to the codex file")
	}
	writeFile(t, codexPath, edited)

	runWeft(t, home, "profile", "use", "hybrid", "--no-watch")

	personalSrc := readFile(t, filepath.Join(personal, "CLAUDE.md"))
	mustContain(t, "write-back reaches source", personalSrc, "# EDITED personal rules via codex")
	// The generated projects block must collapse back to its placeholder, and no
	// attribution markers may leak into the source.
	mustContain(t, "write-back restores placeholder", personalSrc, "<!-- weft:projects -->")
	mustNotContain(t, "write-back leaks markers", personalSrc, "weft:source:begin")

	// Teardown + revert-to-before is handled by the t.Cleanup registered above,
	// so it runs whether this test passes, fails, or aborts via t.Fatal.
}
