package watch

import "sync/atomic"

// ApplyGuard prevents the target watcher from treating weft's own apply
// writes as external edits. Call Lock before writing to targets and Unlock
// after (defer-safe). The target watcher calls Active to decide whether to
// skip reverse-sync.
type ApplyGuard struct {
	active atomic.Bool
}

// Lock marks an apply as in progress.
func (g *ApplyGuard) Lock() { g.active.Store(true) }

// Unlock marks an apply as complete.
func (g *ApplyGuard) Unlock() { g.active.Store(false) }

// Active reports whether an apply is currently writing to targets.
func (g *ApplyGuard) Active() bool { return g.active.Load() }
