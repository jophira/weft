package autosync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jophira/weft/internal/source"

	"github.com/jophira/weft/internal/privatefile"
)

// DefaultInterval is the minimum time between automatic pulls for a source.
const DefaultInterval = 5 * time.Minute

// maxConcurrentSyncs caps how many pulls are in flight at once.
//
// Auto-sync used to start a goroutine and a network connection for every due
// source at once. Each one holds file descriptors, an SSH-agent slot and a
// connection to the remote, so a registry of any size could exhaust the agent,
// hit the host's rate limit, or run the process out of descriptors — and the
// failures land on the user as an unexplained sync error at startup (#281).
//
// Four is chosen for the resource that runs out first: ssh-agent serialises
// signing requests, so more concurrency stops buying wall time well before it
// stops costing descriptors. The work is network-bound, so this is deliberately
// not tied to NumCPU.
const maxConcurrentSyncs = 4

// syncTimeout bounds one source's clone or pull, retries included. Without it a
// remote that accepts a connection and then stalls holds its slot forever, and
// with a bounded pool that stalls every source behind it — the bound turns one
// hung remote into a stuck weft. git.Pull retries with exponential backoff, so
// this is generous enough to cover several attempts.
const syncTimeout = 2 * time.Minute

// State records when each source was last successfully synced.
type State struct {
	Sources map[string]time.Time `json:"sources"`
}

// SyncFunc clones or pulls a source. Returns true when the local tree changed.
// The context carries this source's deadline; an implementation that ignores it
// gives up the timeout guarantee for every source behind it in the pool.
type SyncFunc func(ctx context.Context, s source.Source) (updated bool, err error)

// DefaultStateFilePath returns the path to the sync-state file.
func DefaultStateFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "weft", ".sync_state.json"), nil
}

// ReadState reads the sync state from path.
// Returns an empty State (not an error) when the file does not exist yet.
func ReadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{Sources: make(map[string]time.Time)}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	if s.Sources == nil {
		s.Sources = make(map[string]time.Time)
	}
	return s, nil
}

// WriteState persists s to path, creating parent directories as needed.
func WriteState(path string, s State) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return privatefile.Write(path, data)
}

// ShouldSync reports whether source name is due for a pull as of now.
// A zero interval means always sync (no debounce).
func ShouldSync(s State, name string, now time.Time, interval time.Duration) bool {
	if interval == 0 {
		return true
	}
	last, ok := s.Sources[name]
	if !ok {
		return true
	}
	return now.Sub(last) > interval
}

// MarkSynced returns a copy of s with name's timestamp set to now.
// The original State is not modified.
func MarkSynced(s State, name string, now time.Time) State {
	out := State{Sources: make(map[string]time.Time, len(s.Sources)+1)}
	maps.Copy(out.Sources, s.Sources) // cf. Java: Map.putAll()
	out.Sources[name] = now
	return out
}

// Run pulls auto_pull sources that are past the debounce interval.
// Uses the real clock; for tests use run directly.
func Run(sources []source.Source, stateFile string, interval time.Duration, syncFn SyncFunc, out io.Writer) error {
	return run(sources, stateFile, interval, time.Now(), syncFn, out)
}

// syncResult holds the outcome of a single syncFn call.
type syncResult struct {
	name    string
	updated bool
	err     error
}

// run is the testable core — now is injected so tests can control time.
// Sync failures are printed to out and do not abort remaining sources.
// Returns the first sync error encountered (if any), after processing all sources.
//
// Sources that are due for a pull run concurrently, at most maxConcurrentSyncs
// at a time, each under its own syncTimeout — so total wall time tracks the
// slowest pull rather than their sum, without one large registry opening a
// connection per source at once.
// cf. Java: a fixed-size Executor plus CompletableFuture.allOf(); the buffered
// channel is the idiomatic Go semaphore.
func run(sources []source.Source, stateFile string, interval time.Duration, now time.Time, syncFn SyncFunc, out io.Writer) error {
	return runWithTimeout(sources, stateFile, interval, now, syncFn, out, syncTimeout)
}

// runWithTimeout is run with the per-source deadline injected, so a test can
// use one it can actually wait for. Mirrors the existing injection of now.
func runWithTimeout(
	sources []source.Source,
	stateFile string,
	interval time.Duration,
	now time.Time,
	syncFn SyncFunc,
	out io.Writer,
	timeout time.Duration,
) error {
	state, err := ReadState(stateFile)
	if err != nil {
		return fmt.Errorf("reading sync state: %w", err)
	}

	// Collect sources that actually need syncing before spawning goroutines.
	var due []source.Source
	for _, s := range sources {
		if s.AutoPull && s.Remote != "" && ShouldSync(state, s.Name, now, interval) {
			due = append(due, s)
		}
	}

	// results is sized to avoid blocking; goroutines never wait on a send.
	// cf. Java: a fixed-size LinkedBlockingQueue receiving from a thread pool.
	results := make(chan syncResult, len(due))

	// A buffered channel used as a counting semaphore: a goroutine acquires a
	// slot before touching the network and releases it on the way out, so at
	// most maxConcurrentSyncs transfers are ever in flight.
	sem := make(chan struct{}, maxConcurrentSyncs)

	var wg sync.WaitGroup
	for _, s := range due {
		// wg.Go replaces wg.Add(1)+go+defer wg.Done() — available since Go 1.22
		// loop variable s is per-iteration since Go 1.22, so no capture needed
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			// The deadline starts when the source acquires a slot, not when the
			// batch does: a source that waited behind three others still gets
			// its full timeout rather than a remainder.
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			updated, err := syncFn(ctx, s)
			results <- syncResult{name: s.Name, updated: updated, err: err}
		})
	}

	// Close results once all goroutines have reported back.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results sequentially — only this goroutine writes to state/out.
	var firstErr error
	for r := range results {
		if r.err != nil {
			fmt.Fprintf(out, "  auto-sync %q: %v\n", r.name, r.err)
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		state = MarkSynced(state, r.name, now)
		if r.updated {
			fmt.Fprintf(out, "  ✓ %s updated\n", r.name)
		}
	}

	_ = WriteState(stateFile, state) // non-fatal: worst case we re-sync next run
	return firstErr
}
