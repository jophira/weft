package pidlock

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestAcquireAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.lock")

	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestAcquireBlocksLiveHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.lock")

	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer lock.Release() //nolint:errcheck // release error irrelevant in test teardown

	_, err = Acquire(path)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
}

// TestAcquireAfterRelease verifies the core flock guarantee: once released,
// a new caller can acquire the lock immediately.
func TestAcquireAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.lock")

	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	_ = second.Release()
}

// TestAcquireConcurrent runs 20 goroutines that all attempt Acquire simultaneously.
// Exactly one must succeed; the rest must return ErrLocked. Run with -race.
func TestAcquireConcurrent(t *testing.T) {
	const n = 20
	path := filepath.Join(t.TempDir(), "weft.lock")

	type result struct {
		lock *Lock
		err  error
	}

	results := make([]result, n)
	var wg sync.WaitGroup
	wg.Add(n)

	for i := range n {
		go func(i int) {
			defer wg.Done()
			l, err := Acquire(path)
			results[i] = result{lock: l, err: err}
		}(i)
	}
	wg.Wait()

	var heldLocks []*Lock
	for _, r := range results {
		if r.err == nil {
			heldLocks = append(heldLocks, r.lock)
		} else if !errors.Is(r.err, ErrLocked) {
			t.Errorf("unexpected error (not ErrLocked): %v", r.err)
		}
	}
	for _, l := range heldLocks {
		_ = l.Release()
	}
	if len(heldLocks) != 1 {
		t.Errorf("expected exactly 1 successful Acquire, got %d", len(heldLocks))
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.lock")
	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	// Second call must not panic; error is expected and acceptable.
	_ = lock.Release()
}
