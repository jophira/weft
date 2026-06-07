package pidlock

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
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

// TestAcquireConcurrent verifies that when N goroutines race to acquire the
// same lock file exactly one succeeds and all others receive ErrLocked.
// This is the key correctness property of the O_EXCL fix — the old read-check-
// write sequence could allow multiple goroutines to each observe a stale (or
// absent) file and all proceed to write their PID.
func TestAcquireConcurrent(t *testing.T) {
	const goroutines = 20
	path := filepath.Join(t.TempDir(), "weft.lock")

	type result struct {
		lock *Lock
		err  error
	}

	results := make([]result, goroutines)
	var wg sync.WaitGroup
	// ready synchronises all goroutines to start simultaneously, maximising
	// the chance of exposing races (cf. Java: CountDownLatch).
	ready := make(chan struct{})

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-ready // block until all goroutines are spawned
			l, err := Acquire(path)
			results[idx] = result{lock: l, err: err}
		}(i)
	}

	close(ready) // release all goroutines at once
	wg.Wait()

	var winners []*Lock
	var losers []error
	for _, r := range results {
		if r.err == nil {
			winners = append(winners, r.lock)
		} else {
			losers = append(losers, r.err)
		}
	}

	if len(winners) != 1 {
		t.Errorf("expected exactly 1 winner, got %d", len(winners))
	}
	if len(losers) != goroutines-1 {
		t.Errorf("expected %d losers, got %d", goroutines-1, len(losers))
	}
	for _, err := range losers {
		var locked *ErrLocked
		if ok := asErrLocked(err, &locked); !ok {
			t.Errorf("loser error should be ErrLocked, got %T: %v", err, err)
		}
	}

	if len(winners) == 1 {
		winners[0].Release()
	}
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

// ── ErrLocked.Error ───────────────────────────────────────────────────────────

func TestErrLocked_ErrorMessage(t *testing.T) {
	e := &ErrLocked{Path: "/tmp/weft.lock", HolderPID: 12345}
	msg := e.Error()
	if msg == "" {
		t.Fatal("ErrLocked.Error() returned empty string")
	}
	// Message must include the PID so users know which process to check.
	if !containsStr(msg, "12345") {
		t.Errorf("ErrLocked.Error() = %q, expected PID 12345 in message", msg)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
