//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests cover the parts of `weft autostart` that do not touch the host's
// service manager: reporting an uninstalled state, and the unit entry point's
// behaviour when there is nothing to watch. Actually installing a systemd unit
// or LaunchAgent would mutate the developer's machine, so enable/disable are
// covered by the injected-runner tests in internal/autostart instead.

func TestAutostartStatus_should_report_not_installed_on_a_fresh_home(t *testing.T) {
	home := t.TempDir()

	out := runWeft(t, home, "autostart", "status")

	mustContain(t, "autostart status", out, "Autostart: not installed")
	mustContain(t, "autostart status", out, "weft autostart enable")
}

func TestAutostartRun_should_exit_zero_when_no_profile_is_active(t *testing.T) {
	home := t.TempDir()
	// A configured machine that has simply never activated a profile: the
	// config exists, but carries no active_profile key.
	writeFile(t, filepath.Join(home, ".config", "weft", "config.yaml"), "warn_instruction_size_kb: 96\n")

	// The acceptance criterion from #212: with Restart=on-failure, a non-zero
	// exit here would crash-loop forever on a machine with no active profile.
	// runWeft fails the test on any non-zero exit, so reaching the assertions
	// is itself the proof that the exit code was 0.
	out := runWeft(t, home, "autostart", "run")

	mustContain(t, "autostart run", out, "no active profile set")
	mustContain(t, "autostart run", out, "weft profile use")
}

func TestAutostartRun_should_exit_zero_when_active_profile_is_blank(t *testing.T) {
	home := t.TempDir()
	// An explicitly empty active_profile is the state left behind by a config
	// written before any profile was activated — it must behave like "absent",
	// not like a profile named "".
	writeFile(t, filepath.Join(home, ".config", "weft", "config.yaml"), "active_profile: \"\"\n")

	out := runWeft(t, home, "autostart", "run")

	mustContain(t, "autostart run", out, "no active profile set")
}

func TestAutostartRun_should_exit_zero_when_the_home_never_appears(t *testing.T) {
	// Point HOME at a path that does not exist, standing in for a network or
	// encrypted home that has not mounted. The run must give up with a message
	// naming the file it waited for — and still exit 0, because an unmounted
	// home and a never-configured machine are indistinguishable from here and
	// failing would crash-loop the unit on the latter.
	missing := filepath.Join(t.TempDir(), "not-mounted")

	out := runWeft(t, missing, "autostart", "run", "--wait-for-home", "300ms")

	mustContain(t, "autostart run", out, "did not appear within")
	mustContain(t, "autostart run", out, "config.yaml")
}

// TestAutostartRun_should_hand_off_to_a_manual_profile_use is the "no
// double-watch" acceptance criterion: the autostarted watcher holds the
// singleton lock, so a hand-run `weft profile use <other>` detects it and hands
// the profile over instead of starting a second watcher.
func TestAutostartRun_should_hand_off_to_a_manual_profile_use(t *testing.T) {
	home := t.TempDir()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "CLAUDE.md"), "# rules\n")
	writeFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "# user notes\n")

	runWeft(t, home, "source", "add", "s1", src)
	runWeft(t, home, "profile", "create", "alpha", "--sources", "s1", "--target", "claude-code")
	runWeft(t, home, "profile", "create", "beta", "--sources", "s1", "--target", "claude-code")
	runWeft(t, home, "profile", "use", "alpha", "--no-watch")

	watcher := exec.Command(weftBin, "autostart", "run")
	watcher.Env = hermeticEnv(home)
	watcher.Stdin = strings.NewReader("")
	if err := watcher.Start(); err != nil {
		t.Fatalf("starting autostart run: %v", err)
	}
	t.Cleanup(func() {
		_ = watcher.Process.Kill()
		_, _ = watcher.Process.Wait()
	})

	waitForWatcher(t, home)

	out := runWeft(t, home, "profile", "use", "beta")

	mustContain(t, "profile use during autostart", out, "Handed \"beta\" off to the running weft watcher")
	mustNotContain(t, "profile use during autostart", out, "Watching for changes")
}

// waitForWatcher blocks until `weft status --short` reports a live watcher.
func waitForWatcher(t *testing.T, home string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(runWeft(t, home, "status", "--short"), "watch:on") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("autostarted watcher never became live")
}

func TestAutostartEnable_should_reject_an_unknown_pinned_profile(t *testing.T) {
	home := t.TempDir()

	// A typo must fail here, at the point of installation, rather than in a
	// background service whose only symptom is "weft is not running".
	cmd := exec.Command(weftBin, "autostart", "enable", "--profile", "does-not-exist")
	cmd.Env = hermeticEnv(home)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("expected enable to reject an unknown profile\n%s", out)
	}
	// Nothing may be installed as a side effect of the failed attempt.
	if _, statErr := os.Stat(filepath.Join(home, ".config", "systemd", "user", "weft.service")); statErr == nil {
		t.Fatal("a failed enable left a systemd unit behind")
	}
	if _, statErr := os.Stat(filepath.Join(home, ".config", "weft", "autostart.json")); statErr == nil {
		t.Fatal("a failed enable left autostart metadata behind")
	}
}
