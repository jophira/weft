// Package advice collects the one-line suggestions weft prints at the end of a
// run: the installed-but-not-targeted hint, the instruction-size warning, the
// deprecation notices, and anything else that wants to nudge without failing.
//
// It exists because those hints grew up one at a time, each with its own
// Fprintf and its own idea of when to stay quiet. The shared gate was a single
// `quiet` bool that every watcher re-apply sets, which silenced the process
// where a hint is most useful. Here the gate is per-code throttling instead, so
// a long-running watcher can still speak without repeating itself every 300 ms.
//
// cf. Java: a compiler's DiagnosticListener. Call sites report findings, and one
// place decides what reaches the user.
package advice

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// DefaultMaxPerRun caps how many hints reach the terminal in one run. Everything
// is logged either way; the cap only protects the terminal. Three keeps the tail
// of an apply readable, which is the whole point of a hint over a report.
const DefaultMaxPerRun = 3

// DefaultThrottle is how long a code stays quiet after being shown. A day means
// a watcher running for a week mentions each thing seven times rather than
// several thousand.
const DefaultThrottle = 24 * time.Hour

// Severity ranks a hint. It decides the glyph and the slog level, nothing else:
// advice never changes an exit code, because a suggestion that fails the build
// is not a suggestion.
type Severity int

const (
	Info Severity = iota
	Warn
	Error
)

func (s Severity) String() string {
	switch s {
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	}
	return "unknown"
}

// glyph is the line prefix. It matches the vocabulary already used across weft's
// output so advice does not look like a different program talking.
func (s Severity) glyph() string {
	switch s {
	case Warn:
		return "!"
	case Error:
		return "✗"
	}
	return "•"
}

func (s Severity) slogLevel() slog.Level {
	switch s {
	case Warn:
		return slog.LevelWarn
	case Error:
		return slog.LevelError
	}
	return slog.LevelInfo
}

// Advice is one suggestion.
type Advice struct {
	// Code identifies the hint across releases, e.g. "W012". It is what the user
	// mutes and what documentation refers to, so it must never be reused for a
	// different meaning once shipped.
	Code string
	// Severity ranks the hint.
	Severity Severity
	// Message is one line, no trailing newline, no full stop.
	Message string
	// Fix is the command that acts on it, printed indented beneath. Optional:
	// plenty of hints have no single command that resolves them.
	Fix string
}

// Store persists when each code was last shown, so throttling survives across
// processes. A CLI run lasts milliseconds, so in-memory throttling would do
// nothing at all for the common case of repeated one-shot commands.
type Store interface {
	LastShown(code string) (time.Time, bool)
	MarkShown(code string, at time.Time) error
}

// Options configures a Bus. The zero value is usable: no store (nothing is ever
// throttled), no mutes, DefaultMaxPerRun, DefaultThrottle, real clock.
type Options struct {
	Max      int
	Throttle time.Duration
	Store    Store
	Muted    []string
	// Now is injectable so throttle behaviour is testable without sleeping.
	Now func() time.Time
}

// Bus collects advice during a run and emits it once at the end.
//
// Collect-then-emit rather than print-on-add, because the cap and the ordering
// can only be applied when the full set is known. Printing eagerly would mean
// the first three hints win regardless of severity.
type Bus struct {
	mu       sync.Mutex
	items    []Advice
	seen     map[string]bool
	muted    map[string]bool
	max      int
	throttle time.Duration
	store    Store
	now      func() time.Time
}

// New returns a Bus configured by opts.
func New(opts Options) *Bus {
	b := &Bus{
		seen:     map[string]bool{},
		muted:    map[string]bool{},
		max:      opts.Max,
		throttle: opts.Throttle,
		store:    opts.Store,
		now:      opts.Now,
	}
	if b.max <= 0 {
		b.max = DefaultMaxPerRun
	}
	if b.throttle <= 0 {
		b.throttle = DefaultThrottle
	}
	if b.now == nil {
		b.now = time.Now
	}
	for _, c := range opts.Muted {
		b.muted[c] = true
	}
	return b
}

// Add records a hint. The first occurrence of a code within a run wins;
// duplicates are dropped, so a loop over twelve harnesses that all trip the same
// condition still yields one line.
func (b *Bus) Add(a Advice) {
	if a.Code == "" || a.Message == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seen[a.Code] {
		return
	}
	b.seen[a.Code] = true
	b.items = append(b.items, a)
}

// Len reports how many distinct hints were collected, printed or not. Used by
// tests and by callers that want to know whether a run had anything to say.
func (b *Bus) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items)
}

// Emit writes the surviving hints to w and records every collected hint to the
// log.
//
// Everything reaches the log, including hints that were muted, throttled or cut
// by the cap. That asymmetry is deliberate: the terminal is a scarce surface and
// the log is not, and a hint that never appears anywhere cannot be mined later.
func (b *Bus) Emit(w io.Writer) {
	b.mu.Lock()
	items := make([]Advice, len(b.items))
	copy(items, b.items)
	b.items = nil
	b.seen = map[string]bool{}
	b.mu.Unlock()

	if len(items) == 0 {
		return
	}

	// Highest severity first, insertion order preserved within a severity, so
	// the cap drops the least important rather than the most recent.
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Severity > items[j].Severity
	})

	now := b.now()
	printed := 0
	for _, a := range items {
		reason := b.suppression(a, now)
		if reason == "" && printed < b.max {
			b.print(w, a)
			printed++
			if b.store != nil {
				if err := b.store.MarkShown(a.Code, now); err != nil {
					slog.Debug("advice: recording last-shown failed",
						slog.String("code", a.Code), slog.Any("error", err))
				}
			}
		} else if reason == "" {
			reason = "capped"
		}
		slog.Log(context.Background(), a.Severity.slogLevel(), "advice",
			slog.String("code", a.Code),
			slog.String("severity", a.Severity.String()),
			slog.String("message", a.Message),
			slog.String("suppressed", reason),
		)
	}
}

// suppression returns why a hint should not print, or "" when it should.
func (b *Bus) suppression(a Advice, now time.Time) string {
	if b.muted[a.Code] {
		return "muted"
	}
	if b.store == nil {
		return ""
	}
	last, ok := b.store.LastShown(a.Code)
	if !ok {
		return ""
	}
	if now.Sub(last) < b.throttle {
		return "throttled"
	}
	return ""
}

func (b *Bus) print(w io.Writer, a Advice) {
	fmt.Fprintf(w, "%s %s [%s]\n", a.Severity.glyph(), a.Message, a.Code)
	if a.Fix != "" {
		fmt.Fprintf(w, "  %s\n", a.Fix)
	}
}

// ── package-level default ─────────────────────────────────────────────────────

// The CLI collects advice from a dozen call sites spread across cmd/ and
// internal/, none of which share a receiver. Threading a *Bus through all of
// them would be churn for no benefit, so the package keeps a default the same
// way log/slog does.

var (
	defaultMu  sync.RWMutex
	defaultBus = New(Options{})
)

// SetDefault replaces the package-level bus. Called once from the CLI root after
// config is read, so mutes and the throttle store are in place before any
// command runs.
func SetDefault(b *Bus) {
	if b == nil {
		return
	}
	defaultMu.Lock()
	defaultBus = b
	defaultMu.Unlock()
}

// Default returns the package-level bus.
func Default() *Bus {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultBus
}

// Add records a hint on the default bus.
func Add(a Advice) { Default().Add(a) }

// Emit flushes the default bus to w.
func Emit(w io.Writer) { Default().Emit(w) }
