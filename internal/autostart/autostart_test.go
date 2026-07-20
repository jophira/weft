package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeBin creates an executable stand-in for the weft binary and returns its path.
func fakeBin(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("writing fake binary: %v", err)
	}
	return p
}

func TestOptionsValidate_should_reject_unusable_binary_paths(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, "weft")

	tests := []struct {
		name    string
		opts    Options
		wantErr string
	}{
		{"empty", Options{}, "binary path is empty"},
		{"relative", Options{BinPath: "bin/weft"}, "not absolute"},
		{"missing", Options{BinPath: filepath.Join(dir, "nope")}, "not usable"},
		{"relative config", Options{BinPath: bin, ConfigFile: "cfg.yaml"}, "not absolute"},
		{"valid", Options{BinPath: bin}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestStatusProfileMode_should_distinguish_pinned_from_follow_active(t *testing.T) {
	if got := (Status{}).ProfileMode(); got != "follow-active" {
		t.Errorf("empty profile ProfileMode = %q, want follow-active", got)
	}
	if got := (Status{Profile: "work"}).ProfileMode(); got != "pinned" {
		t.Errorf("pinned ProfileMode = %q, want pinned", got)
	}
}

func TestMetaRoundTrip_should_persist_and_clear(t *testing.T) {
	cfgDir := t.TempDir()
	if readMeta(cfgDir) != nil {
		t.Fatal("readMeta on empty dir should be nil")
	}

	want := meta{Bin: "/usr/local/bin/weft", Profile: "work", Mechanism: "test", UnitPath: "/u", InstalledAt: nowUTC()}
	if err := writeMeta(cfgDir, want); err != nil {
		t.Fatalf("writeMeta: %v", err)
	}
	got := readMeta(cfgDir)
	if got == nil || got.Bin != want.Bin || got.Profile != want.Profile {
		t.Fatalf("readMeta = %+v, want %+v", got, want)
	}

	if err := clearMeta(cfgDir); err != nil {
		t.Fatalf("clearMeta: %v", err)
	}
	if readMeta(cfgDir) != nil {
		t.Fatal("readMeta after clear should be nil")
	}
	// Clearing twice must not error — disable is expected to be idempotent.
	if err := clearMeta(cfgDir); err != nil {
		t.Fatalf("second clearMeta: %v", err)
	}
}

func TestReadMeta_should_treat_corrupt_sidecar_as_absent(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.WriteFile(metaPath(cfgDir), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("writing corrupt sidecar: %v", err)
	}
	if readMeta(cfgDir) != nil {
		t.Fatal("corrupt sidecar should read as absent, not panic or return junk")
	}
}

func TestDescribe_should_flag_a_missing_binary_as_stale(t *testing.T) {
	cfgDir := t.TempDir()
	gone := filepath.Join(cfgDir, "moved-away", "weft")
	if err := writeMeta(cfgDir, meta{Bin: gone, UnitPath: "/u"}); err != nil {
		t.Fatalf("writeMeta: %v", err)
	}

	st := Status{Installed: true}
	describe(cfgDir, &st, "ExecStart="+gone)

	if !st.Stale {
		t.Fatal("describe should mark a vanished binary stale")
	}
	if !containsSubstring(st.Notes, "no longer exists") {
		t.Fatalf("notes = %v, want one mentioning the missing binary", st.Notes)
	}
}

func TestDescribe_should_flag_a_unit_that_lost_its_binary_reference(t *testing.T) {
	cfgDir := t.TempDir()
	bin := fakeBin(t, cfgDir, "weft")
	if err := writeMeta(cfgDir, meta{Bin: bin, UnitPath: "/u"}); err != nil {
		t.Fatalf("writeMeta: %v", err)
	}

	st := Status{Installed: true}
	describe(cfgDir, &st, "ExecStart=/some/other/weft autostart run")

	if !st.Stale {
		t.Fatal("describe should mark a hand-edited unit stale")
	}
	if !containsSubstring(st.Notes, "edited outside weft") {
		t.Fatalf("notes = %v, want one about external edits", st.Notes)
	}
}

func TestDescribe_should_report_an_unowned_unit(t *testing.T) {
	st := Status{Installed: true}
	describe(t.TempDir(), &st, "ExecStart=/usr/bin/weft")

	if st.Stale {
		t.Error("an unowned unit is not stale — weft simply did not install it")
	}
	if !containsSubstring(st.Notes, "no record of installing it") {
		t.Fatalf("notes = %v, want one about the missing record", st.Notes)
	}
}

func TestDescribe_should_stay_quiet_when_the_recorded_binary_is_the_running_one(t *testing.T) {
	cfgDir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable: %v", err)
	}
	if err := writeMeta(cfgDir, meta{Bin: self, Profile: "work", UnitPath: "/u"}); err != nil {
		t.Fatalf("writeMeta: %v", err)
	}

	st := Status{Installed: true}
	describe(cfgDir, &st, "ExecStart="+self)

	if st.Stale || len(st.Notes) != 0 {
		t.Fatalf("healthy install produced stale=%v notes=%v", st.Stale, st.Notes)
	}
	if st.Profile != "work" || st.BinPath != self {
		t.Fatalf("describe did not copy metadata: %+v", st)
	}
}

func TestIsRivalBinary_should_only_compare_same_named_programs(t *testing.T) {
	dir := t.TempDir()
	a := fakeBin(t, dir, "weft")
	sub := filepath.Join(dir, "other")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	b := fakeBin(t, sub, "weft")

	if !isRivalBinary(a, b) {
		t.Error("two weft binaries in different directories are rivals")
	}
	if isRivalBinary(a, a) {
		t.Error("a binary is not its own rival")
	}
	// A `go test` binary must not be reported as a competing weft install.
	if isRivalBinary(fakeBin(t, dir, "autostart.test"), a) {
		t.Error("differently named programs are not rivals")
	}
}

func TestDescribe_should_flag_a_second_weft_binary(t *testing.T) {
	cfgDir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable: %v", err)
	}
	// Record a binary that shares the running executable's name but is a
	// different file — the "several weft binaries" case from #212.
	rival := fakeBin(t, cfgDir, filepath.Base(self))
	if err := writeMeta(cfgDir, meta{Bin: rival, UnitPath: "/u"}); err != nil {
		t.Fatalf("writeMeta: %v", err)
	}

	st := Status{Installed: true}
	describe(cfgDir, &st, "ExecStart="+rival)

	if st.Stale {
		t.Error("a working unit pointing at another copy is not stale, just worth noting")
	}
	if !containsSubstring(st.Notes, "but you are running") {
		t.Fatalf("notes = %v, want one about the competing binary", st.Notes)
	}
}

func TestSameFile_should_follow_symlinks(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, "weft")
	link := filepath.Join(dir, "weft-link")
	if err := os.Symlink(bin, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !sameFile(bin, link) {
		t.Fatal("sameFile should see a symlink and its target as the same binary")
	}
	if sameFile(bin, fakeBin(t, dir, "other")) {
		t.Fatal("sameFile should distinguish two real binaries")
	}
}

func TestWaitForPath_should_return_once_the_path_appears(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "home")
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = os.MkdirAll(dir, 0o755)
	}()
	if err := WaitForPath(dir, 2*time.Second, 5*time.Millisecond); err != nil {
		t.Fatalf("WaitForPath: %v", err)
	}
}

func TestWaitForPath_should_wait_for_a_file_not_just_a_directory(t *testing.T) {
	// The caller waits on config.yaml specifically: a directory can be
	// conjured by any MkdirAll under an unmounted home, a file cannot.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = os.WriteFile(cfg, []byte("active_profile: work\n"), 0o644)
	}()
	if err := WaitForPath(cfg, 2*time.Second, 5*time.Millisecond); err != nil {
		t.Fatalf("WaitForPath: %v", err)
	}
}

func TestWaitForPath_should_time_out_with_a_named_path(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never")
	err := WaitForPath(missing, 30*time.Millisecond, 5*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForPath should time out on a directory that never appears")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q should name the path it waited for", err)
	}
}

func TestWaitForPath_should_reject_a_non_positive_interval(t *testing.T) {
	// A zero interval would spin the CPU for the whole timeout.
	if err := WaitForPath(t.TempDir(), time.Second, 0); err == nil {
		t.Fatal("WaitForPath should reject a zero poll interval")
	}
}

func TestNew_should_reject_an_empty_config_dir(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("New(\"\") should fail rather than default to an unexpected location")
	}
}

func containsSubstring(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
