package pidlock

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestAcquireAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.lock")

	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading lock file: %v", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil || pid != os.Getpid() {
		t.Fatalf("lock file contains %q, want PID %d", data, os.Getpid())
	}

	lock.Release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("lock file should not exist after Release")
	}
}

func TestAcquireBlocksLiveHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.lock")

	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer lock.Release()

	_, err = Acquire(path)
	if err == nil {
		t.Fatal("second Acquire should fail while first lock is held")
	}
	var locked *ErrLocked
	if ok := asErrLocked(err, &locked); !ok {
		t.Fatalf("expected ErrLocked, got %T: %v", err, err)
	}
	if locked.HolderPID != os.Getpid() {
		t.Fatalf("HolderPID = %d, want %d", locked.HolderPID, os.Getpid())
	}
}

func TestAcquireTakesStaleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.lock")

	// Write a stale PID that is guaranteed not to be alive.
	// PID 0 is the idle process — no user process can have it; kill(0, 0) checks
	// the caller's entire process group, not a specific dead process, but writing
	// a non-existent PID like math.MaxInt32 is safer.
	if err := os.WriteFile(path, []byte("99999999"), 0o644); err != nil {
		t.Fatalf("writing stale lock: %v", err)
	}

	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire on stale lock: %v", err)
	}
	lock.Release()
}

func TestReleaseIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.lock")
	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	lock.Release()
	lock.Release() // must not panic
}

// asErrLocked is a type-assertion helper (errors.As requires a pointer to the
// target type, which Go generics can't infer cleanly in table-driven tests).
func asErrLocked(err error, target **ErrLocked) bool {
	e, ok := err.(*ErrLocked)
	if ok {
		*target = e
	}
	return ok
}
