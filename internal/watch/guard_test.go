package watch_test

import (
	"sync"
	"testing"

	"github.com/jophira/weft/internal/watch"
)

func TestApplyGuard_InitiallyInactive(t *testing.T) {
	var g watch.ApplyGuard
	if g.Active() {
		t.Error("new guard should be inactive")
	}
}

func TestApplyGuard_LockActivates(t *testing.T) {
	var g watch.ApplyGuard
	g.Lock()
	if !g.Active() {
		t.Error("guard should be active after Lock")
	}
}

func TestApplyGuard_UnlockDeactivates(t *testing.T) {
	var g watch.ApplyGuard
	g.Lock()
	g.Unlock()
	if g.Active() {
		t.Error("guard should be inactive after Unlock")
	}
}

func TestApplyGuard_DeferUnlock(t *testing.T) {
	var g watch.ApplyGuard
	func() {
		g.Lock()
		defer g.Unlock()
	}()
	if g.Active() {
		t.Error("guard should be inactive after deferred Unlock")
	}
}

func TestApplyGuard_ConcurrentAccess(t *testing.T) {
	var g watch.ApplyGuard
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Lock()
			_ = g.Active()
			g.Unlock()
		}()
	}
	wg.Wait()
	if g.Active() {
		t.Error("guard should be inactive after all goroutines complete")
	}
}
