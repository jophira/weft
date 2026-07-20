//go:build linux

package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSystemctl records every invocation and replays scripted responses, so
// the lifecycle can be exercised without touching the developer's real
// systemd. cf. Mockito: a spy that both records and stubs.
type fakeSystemctl struct {
	calls  []string
	stdout map[string]string // command line -> output
	fail   map[string]bool   // command line -> return an error
}

func newFakeSystemctl() *fakeSystemctl {
	return &fakeSystemctl{stdout: map[string]string{}, fail: map[string]bool{}}
}

func (f *fakeSystemctl) run(name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if f.fail[line] {
		return nil, fmt.Errorf("fake failure: %s", line)
	}
	return []byte(f.stdout[line]), nil
}

func (f *fakeSystemctl) called(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// newTestService wires a systemdService onto temp directories and a fake runner.
func newTestService(t *testing.T) (*systemdService, *fakeSystemctl, Options) {
	t.Helper()
	cfgDir := t.TempDir()
	unitDir := filepath.Join(t.TempDir(), "systemd", "user")
	fake := newFakeSystemctl()
	svc := &systemdService{cfgDir: cfgDir, unitDir: unitDir, run: fake.run}
	return svc, fake, Options{BinPath: fakeBin(t, cfgDir, "weft")}
}

func TestSystemdEnable_should_write_the_unit_and_start_it(t *testing.T) {
	svc, fake, opts := newTestService(t)

	if err := svc.Enable(opts); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	data, err := os.ReadFile(svc.unitPath())
	if err != nil {
		t.Fatalf("unit not written: %v", err)
	}
	if !strings.Contains(string(data), opts.BinPath) {
		t.Errorf("unit does not reference the binary:\n%s", data)
	}
	if !fake.called("daemon-reload") || !fake.called("enable --now "+UnitName) {
		t.Fatalf("Enable did not reload and start the unit: %v", fake.calls)
	}
	if fake.called("loginctl") {
		t.Fatal("linger must never be enabled unless explicitly requested")
	}
	if m := readMeta(svc.cfgDir); m == nil || m.Bin != opts.BinPath {
		t.Fatalf("metadata not recorded: %+v", m)
	}
}

func TestSystemdEnable_should_enable_linger_only_on_request(t *testing.T) {
	svc, fake, opts := newTestService(t)
	opts.Linger = true

	if err := svc.Enable(opts); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !fake.called("loginctl enable-linger") {
		t.Fatalf("--linger did not enable linger: %v", fake.calls)
	}
}

func TestSystemdEnable_should_be_idempotent_and_repoint_a_moved_binary(t *testing.T) {
	svc, _, opts := newTestService(t)
	if err := svc.Enable(opts); err != nil {
		t.Fatalf("first Enable: %v", err)
	}

	// Simulate an upgrade that put weft somewhere else — re-running enable is
	// the documented fix for a stale unit, so it must overwrite, not fail.
	moved := fakeBin(t, svc.cfgDir, "weft-v2")
	if err := svc.Enable(Options{BinPath: moved}); err != nil {
		t.Fatalf("second Enable: %v", err)
	}

	data, err := os.ReadFile(svc.unitPath())
	if err != nil {
		t.Fatalf("reading unit: %v", err)
	}
	if !strings.Contains(string(data), moved) {
		t.Fatalf("unit was not re-pointed at the new binary:\n%s", data)
	}
}

func TestSystemdEnable_should_reject_a_missing_binary(t *testing.T) {
	svc, fake, _ := newTestService(t)

	err := svc.Enable(Options{BinPath: filepath.Join(svc.cfgDir, "absent")})
	if err == nil {
		t.Fatal("Enable should reject a binary path that does not exist")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("Enable touched systemd despite invalid options: %v", fake.calls)
	}
}

func TestSystemdEnable_should_roll_back_the_unit_when_systemd_rejects_it(t *testing.T) {
	svc, fake, opts := newTestService(t)
	fake.fail["systemctl --user enable --now "+UnitName] = true

	if err := svc.Enable(opts); err == nil {
		t.Fatal("Enable should surface a systemd failure")
	}
	// A unit left on disk but never enabled is the orphan state disable is
	// supposed to make impossible.
	if _, err := os.Stat(svc.unitPath()); !os.IsNotExist(err) {
		t.Fatalf("failed Enable left a unit behind: %v", err)
	}
	if readMeta(svc.cfgDir) != nil {
		t.Fatal("failed Enable recorded metadata")
	}
}

func TestSystemdStatus_should_report_installed_and_running(t *testing.T) {
	svc, fake, opts := newTestService(t)
	if err := svc.Enable(opts); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	fake.stdout["systemctl --user is-active "+UnitName] = "active\n"

	st, err := svc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running {
		t.Fatalf("Status = %+v, want installed and running", st)
	}
	if st.Stale || len(st.Notes) != 0 {
		t.Fatalf("healthy install reported stale=%v notes=%v", st.Stale, st.Notes)
	}
	if st.ProfileMode() != "follow-active" {
		t.Errorf("unpinned unit should be follow-active, got %s", st.ProfileMode())
	}
}

func TestSystemdStatus_should_report_installed_but_stopped(t *testing.T) {
	svc, fake, opts := newTestService(t)
	if err := svc.Enable(opts); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	// systemctl is-active exits non-zero for a stopped unit; the output, not
	// the exit code, is the answer.
	fake.stdout["systemctl --user is-active "+UnitName] = "inactive\n"
	fake.fail["systemctl --user is-active "+UnitName] = false

	st, err := svc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || st.Running {
		t.Fatalf("Status = %+v, want installed but not running", st)
	}
}

func TestSystemdStatus_should_report_a_stale_binary_rather_than_failing(t *testing.T) {
	svc, _, opts := newTestService(t)
	if err := svc.Enable(opts); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := os.Remove(opts.BinPath); err != nil {
		t.Fatalf("removing binary: %v", err)
	}

	st, err := svc.Status()
	if err != nil {
		t.Fatalf("Status must not fail on a stale unit: %v", err)
	}
	if !st.Stale {
		t.Fatalf("Status = %+v, want Stale", st)
	}
	if !containsSubstring(st.Notes, "autostart enable") {
		t.Fatalf("notes = %v, want a re-point instruction", st.Notes)
	}
}

func TestSystemdStatus_should_report_not_installed_when_absent(t *testing.T) {
	svc, _, _ := newTestService(t)

	st, err := svc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Installed || st.Running {
		t.Fatalf("Status = %+v, want nothing installed", st)
	}
}

func TestSystemdDisable_should_leave_no_orphan_unit(t *testing.T) {
	svc, fake, opts := newTestService(t)
	if err := svc.Enable(opts); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if err := svc.Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if !fake.called("disable --now " + UnitName) {
		t.Fatalf("Disable did not stop the unit: %v", fake.calls)
	}
	if _, err := os.Stat(svc.unitPath()); !os.IsNotExist(err) {
		t.Fatalf("unit file survived disable: %v", err)
	}
	if readMeta(svc.cfgDir) != nil {
		t.Fatal("metadata survived disable")
	}

	// The acceptance criterion: re-running status after disable sees nothing.
	st, err := svc.Status()
	if err != nil {
		t.Fatalf("Status after disable: %v", err)
	}
	if st.Installed || st.Running || len(st.Notes) != 0 {
		t.Fatalf("orphan left behind: %+v", st)
	}
}

func TestSystemdDisable_should_report_not_installed_when_nothing_to_remove(t *testing.T) {
	svc, _, _ := newTestService(t)

	if err := svc.Disable(); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Disable = %v, want ErrNotInstalled", err)
	}
}

func TestSystemdDisable_should_clean_up_after_a_hand_deleted_unit(t *testing.T) {
	svc, _, opts := newTestService(t)
	if err := svc.Enable(opts); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	// User deleted the unit file by hand; weft's metadata is now the only
	// trace. Disable must still clear it rather than leaving a phantom record.
	if err := os.Remove(svc.unitPath()); err != nil {
		t.Fatalf("removing unit: %v", err)
	}

	if err := svc.Disable(); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Disable = %v, want ErrNotInstalled", err)
	}
	if readMeta(svc.cfgDir) != nil {
		t.Fatal("metadata survived disable of a hand-deleted unit")
	}
}
