//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/testutil"
)

// execWeft builds a hermetic weft command for best-effort teardown (no test
// failure on non-zero exit, unlike runWeft).
func execWeft(home string, args ...string) *exec.Cmd {
	cmd := exec.Command(weftBin, args...)
	cmd.Env = hermeticEnv(home)
	cmd.Stdin = strings.NewReader("")
	return cmd
}

// TestResolveEndToEnd drives `weft rules resolve` through the real binary against
// a hermetic $HOME:
//
//	2 annotated sources (pers, work) + 1 un-annotated (raw) → profile create
//	→ profile use → resolve a Java repo (both annotated sources contribute in
//	priority order; the un-annotated source contributes nothing — the black-box
//	work-tech guard) → resolve a plain repo (only always-on rules) → switch the
//	active profile and re-resolve (the profile-use → resolve handoff).
func TestResolveEndToEnd(t *testing.T) {
	home := t.TempDir()

	// Guardrail: never touch the developer's real home if the env override regresses.
	if realHome, err := os.UserHomeDir(); err == nil && (home == realHome || strings.HasPrefix(realHome, home+string(os.PathSeparator))) {
		t.Fatalf("test $HOME %q is not isolated from the real home %q — aborting", home, realHome)
	}

	// ── 1. Sources ─────────────────────────────────────────────────────────────
	srcRoot := t.TempDir()
	pers := filepath.Join(srcRoot, "pers")
	work := filepath.Join(srcRoot, "work")
	raw := filepath.Join(srcRoot, "raw")

	// pers + work are fully annotated: an always-on common rule and a java rule
	// (detects pom.xml) that extends it. Bodies differ so the bundle is unambiguous.
	writeFile(t, filepath.Join(pers, "CLAUDE.md"), "# pers instructions")
	writeFile(t, filepath.Join(pers, "common.md"), testutil.RuleFile("common", "true", "PERS_COMMON"))
	writeFile(t, filepath.Join(pers, "java.md"), testutil.RuleFile("java", "'pom.xml' in files", "PERS_JAVA", "common"))

	writeFile(t, filepath.Join(work, "CLAUDE.md"), "# work instructions")
	writeFile(t, filepath.Join(work, "common.md"), testutil.RuleFile("common", "true", "WORK_COMMON"))
	writeFile(t, filepath.Join(work, "java.md"), testutil.RuleFile("java", "'pom.xml' in files", "WORK_JAVA", "common"))

	// raw looks like a rules source but carries NO front-matter — exactly work-tech
	// before annotation. It must contribute nothing.
	writeFile(t, filepath.Join(raw, "CLAUDE.md"), "# raw instructions")
	writeFile(t, filepath.Join(raw, "common.md"), "# raw common\n\nRAW_COMMON no front-matter\n")

	// ── 2. Register (lowest → highest priority) and build profiles ─────────────
	t.Cleanup(func() {
		for _, args := range [][]string{
			{"profile", "delete", "hybrid"},
			{"profile", "delete", "onlypers"},
			{"source", "remove", "pers"},
			{"source", "remove", "work"},
			{"source", "remove", "raw"},
		} {
			cmd := execWeft(home, args...)
			_ = cmd.Run() // best-effort teardown
		}
	})

	runWeft(t, home, "source", "add", "pers", pers, "--priority", "10")
	runWeft(t, home, "source", "add", "work", work, "--priority", "20")
	runWeft(t, home, "source", "add", "raw", raw, "--priority", "30")

	runWeft(t, home, "profile", "create", "hybrid", "--sources", "pers,work,raw", "--target", "claude-code")
	runWeft(t, home, "profile", "create", "onlypers", "--sources", "pers", "--target", "claude-code")

	// ── 3. Activate hybrid and resolve a Java repo ─────────────────────────────
	runWeft(t, home, "profile", "use", "hybrid", "--no-watch")

	javaRepo := t.TempDir()
	writeFile(t, filepath.Join(javaRepo, "pom.xml"), "")
	javaBundle := runWeft(t, home, "rules", "resolve", javaRepo, "--no-work")

	// Both annotated sources contribute, low priority first (pers before work),
	// dependency before dependent (common before java) within each source.
	mustOrder(t, "java resolve order", javaBundle, "PERS_COMMON", "PERS_JAVA", "WORK_COMMON", "WORK_JAVA")
	// The un-annotated source contributes nothing — silently, as in the real gap.
	mustNotContain(t, "un-annotated source is inert", javaBundle, "RAW_COMMON")

	// ── 4. Resolve a plain repo: only always-on rules ──────────────────────────
	plainRepo := t.TempDir()
	writeFile(t, filepath.Join(plainRepo, "README.md"), "")
	plainBundle := runWeft(t, home, "rules", "resolve", plainRepo, "--no-work")

	mustContain(t, "plain resolve has always-on", plainBundle, "PERS_COMMON")
	mustContain(t, "plain resolve has always-on", plainBundle, "WORK_COMMON")
	mustNotContain(t, "plain repo does not select java", plainBundle, "PERS_JAVA")
	mustNotContain(t, "plain repo does not select java", plainBundle, "WORK_JAVA")

	// ── 5. Switch the active profile and re-resolve (profile-use → resolve) ─────
	runWeft(t, home, "profile", "use", "onlypers", "--no-watch")
	switched := runWeft(t, home, "rules", "resolve", javaRepo, "--no-work")

	mustContain(t, "after switch, pers still contributes", switched, "PERS_JAVA")
	mustNotContain(t, "after switch, work drops out", switched, "WORK_JAVA")
}
