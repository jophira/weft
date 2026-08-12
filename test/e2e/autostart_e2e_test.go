//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
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

	// The watcher's own output is the first thing worth having when it fails to
	// come up, so it is captured rather than discarded.
	watcherOut := &lockedBuffer{}
	watcher := exec.Command(weftBin, "autostart", "run")
	watcher.Env = hermeticEnv(home)
	watcher.Stdin = strings.NewReader("")
	watcher.Stdout = watcherOut
	watcher.Stderr = watcherOut
	if err := watcher.Start(); err != nil {
		t.Fatalf("starting autostart run: %v", err)
	}
	t.Cleanup(func() {
		_ = watcher.Process.Kill()
		_, _ = watcher.Process.Wait()
	})

	waitForWatcher(t, home, watcher, watcherOut)

	out := runWeft(t, home, "profile", "use", "beta")

	mustContain(t, "profile use during autostart", out, "Handed \"beta\" off to the running weft watcher")
	mustNotContain(t, "profile use during autostart", out, "Watching for changes")
}

// watcherDeadline is how long the autostarted watcher gets to report itself live.
//
// It is deliberately still a fixed 20s. The one observed failure (#224, on
// windows-latest) hit this deadline, and 20s is generous for a process spawn
// even on a cold runner, so raising it would hide the next occurrence rather
// than explain it. The dump below is what decides whether a slow start or a
// watcher that never comes up is the real cause; the deadline is worth revisiting
// once a failure has been read rather than guessed at.
const watcherDeadline = 20 * time.Second

// waitForWatcher blocks until `weft status --short` reports a live watcher.
//
// On timeout it dumps everything needed to tell a slow start from a watcher that
// never started: whether the process is still running, what it printed, the
// watcher log, and the full status output. The original bare t.Fatal proved only
// that the watcher did not report live within the deadline, which is the one
// thing the timeout already told you (#224).
func waitForWatcher(t *testing.T, home string, watcher *exec.Cmd, watcherOut *lockedBuffer) {
	t.Helper()

	start := time.Now()
	deadline := start.Add(watcherDeadline)
	var polls int
	var lastStatus string
	// Poll before testing the deadline, so the dump always carries a real status
	// reading. A deadline-first loop can report a timeout having never asked, and
	// "no output" from a poll that never happened is the same unreadable failure
	// this change exists to remove.
	for {
		lastStatus = runWeft(t, home, "status", "--short")
		polls++
		if strings.Contains(lastStatus, "watch:on") {
			return
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("autostarted watcher never became live after %s (%d polls)\n"+
		"  process:  %s\n"+
		"── weft status --short (last poll) ──\n%s\n"+
		"── watcher stdout+stderr ──\n%s\n"+
		"── %s ──\n%s\n"+
		"── weft status (full) ──\n%s",
		time.Since(start).Round(time.Millisecond), polls,
		watcherState(watcher),
		indent(lastStatus),
		indent(watcherOut.String()),
		watcherLogPath(home), indent(readWatcherLog(home)),
		indent(weftOutput(home, "status")))
}

// watcherState reports whether the watcher process is still running, and how it
// exited if not. A process that has already exited is a product bug; one that is
// still running and simply has not reported live is a timing problem. The bare
// message could not tell these apart, which is what made #224 undiagnosable.
func watcherState(watcher *exec.Cmd) string {
	if watcher.ProcessState == nil {
		// Signal 0 probes liveness without delivering anything. Unsupported on
		// Windows, where a nil ProcessState is the only signal available.
		if runtime.GOOS != "windows" && watcher.Process != nil {
			if err := watcher.Process.Signal(syscall.Signal(0)); err != nil {
				return fmt.Sprintf("pid %d not signalable: %v", watcher.Process.Pid, err)
			}
		}
		return fmt.Sprintf("still running (pid %d)", watcher.Process.Pid)
	}
	return fmt.Sprintf("already exited: %s", watcher.ProcessState)
}

// watcherLogPath mirrors internal/logger's default location. It is duplicated
// rather than imported because this package drives the binary as a black box and
// must not depend on weft's internals.
func watcherLogPath(home string) string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "weft", "weft.log")
	}
	return filepath.Join(home, ".local", "share", "weft", "weft.log")
}

func readWatcherLog(home string) string {
	data, err := os.ReadFile(watcherLogPath(home))
	if err != nil {
		return fmt.Sprintf("(unreadable: %v)", err)
	}
	if len(data) == 0 {
		return "(empty)"
	}
	return string(data)
}

// weftOutput runs the binary for diagnostics only, returning whatever came back
// rather than failing the test. runWeft is no use on a failure path: it calls
// t.Fatalf on a non-zero exit, which would replace the dump being assembled.
func weftOutput(home string, args ...string) string {
	cmd := exec.Command(weftBin, args...)
	cmd.Env = hermeticEnv(home)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s\n(exit: %v)", out, err)
	}
	return string(out)
}

// indent keeps each dumped section visibly nested under its heading, since Go
// test output already indents the first line of a Fatalf and nothing else.
func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return "    (no output)"
	}
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

// lockedBuffer collects a child process's output for later inspection. os/exec
// writes from a goroutine of its own, so the test goroutine cannot read a plain
// bytes.Buffer without racing it.
//
// cf. Java: a StringBuffer, where the synchronisation is part of the type rather
// than something the caller adds.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
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
