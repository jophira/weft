package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestApply_KeepsExecuteBit is the #309 regression: a staged script must reach
// the harness runnable, both when it is new and when its source gains the bit
// after weft has already projected it with the same content.
func TestApply_KeepsExecuteBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no execute bits on Windows")
	}
	f := newApplyFixture(t)
	h := &GenericHarness{detection: resolved(f.target), name: "test-harness"}
	apply := func(execRel string) {
		t.Helper()
		staged := t.TempDir()
		for _, rel := range []string{"skills/x/new.sh", "skills/x/later.sh"} {
			write(t, filepath.Join(staged, rel), "#!/bin/sh\n")
		}
		for _, rel := range []string{"skills/x/new.sh", execRel} {
			if err := os.Chmod(filepath.Join(staged, rel), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.Apply(staged, f.ctx); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
	mode := func(rel string) os.FileMode {
		t.Helper()
		info, err := os.Stat(filepath.Join(f.target, rel))
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}

	apply("skills/x/new.sh")
	if got := mode("skills/x/new.sh"); got&0o100 == 0 {
		t.Errorf("new script projected as %v, want owner execute", got)
	}
	if got := mode("skills/x/later.sh"); got&0o111 != 0 {
		t.Errorf("non-executable source projected as %v", got)
	}

	apply("skills/x/later.sh") // same content, source gained the bit
	if got := mode("skills/x/later.sh"); got&0o100 == 0 {
		t.Errorf("script whose source gained the bit is %v, want owner execute", got)
	}
}
