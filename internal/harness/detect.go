package harness

import (
	"os/exec"
	"strings"

	"github.com/jophira/weft/internal/locate"
)

// DetectVia names the signal that satisfied a Detect call.
//
// Detect returns a bare bool, which is all Apply needs but not enough for the
// CLI: "found ~/.aider" and "found aider on PATH" are different facts, and
// reporting the first when the second is true sends users looking in a
// directory that may not exist.
type DetectVia uint8

const (
	// DetectNone means the harness was not found.
	DetectNone DetectVia = iota
	// DetectConfigDir means one of the harness's config roots exists on disk.
	DetectConfigDir
	// DetectBinary means the harness's executable was found on PATH.
	DetectBinary
)

// detectSpec declares the signals that identify one harness.
//
// Harnesses return this from detectSignals() rather than storing it in a field,
// so the zero value of every adapter (&ClaudeCode{}, &Aider{}) stays usable
// without a constructor.
type detectSpec struct {
	// binaries are looked up on PATH, in order, and the first hit wins. Empty
	// skips the check, for harnesses that ship no CLI entry point. It is a slice
	// rather than a single name because a tool can rename its entry point, or
	// ship under two names during a transition, and the alternative name is then
	// the only signal a fresh install offers.
	binaries []string
	// candidates are config roots, probed in order. os.Stat accepts files as
	// well as directories, so a marker config file is a valid candidate.
	candidates []locate.Candidate
}

// detection is the shared Detect implementation plus the record of what matched.
//
// Adapters embed it by value, so it must be useful at its zero value: an
// unrun detection reports DetectNone, never a stale hit.
//
// cf. Java: an abstract base holding the common algorithm and its result state,
// except Go composes it in rather than inheriting, and the embedded methods are
// promoted onto the outer type automatically.
type detection struct {
	root string    // config root, resolved by run
	bin  string    // absolute binary path, when found on PATH
	via  DetectVia // which signal matched
}

// run probes spec's signals in priority order and records the winner.
//
// The config directory is preferred over the binary because it pinpoints the
// exact root to write to. A binary hit still primes root with the first
// candidate, so Apply knows where to create the directory.
func (d *detection) run(spec detectSpec) bool {
	if p, ok := locate.First(spec.candidates); ok {
		d.root, d.bin, d.via = p, "", DetectConfigDir
		return true
	}
	for _, name := range spec.binaries {
		if name == "" {
			continue
		}
		if p, err := exec.LookPath(name); err == nil {
			if paths := locate.All(spec.candidates); len(paths) > 0 {
				d.root = paths[0]
			}
			d.bin, d.via = p, DetectBinary
			return true
		}
	}
	d.via = DetectNone
	return false
}

// DetectedVia implements DetectReporter, returning the signal that matched and
// the concrete evidence for it (a config path, or a binary path).
func (d *detection) DetectedVia() (DetectVia, string) {
	switch d.via {
	case DetectConfigDir:
		return DetectConfigDir, locate.Tilde(d.root)
	case DetectBinary:
		return DetectBinary, locate.Tilde(d.bin)
	default:
		return DetectNone, ""
	}
}

// detectedRoot returns the config root resolved by the last run, or "" when the
// harness has not been detected. Apply uses it to avoid re-probing.
func (d *detection) detectedRoot() string {
	if d.via == DetectNone {
		return ""
	}
	return d.root
}

// resolved returns a detection pre-seeded with root, as though a config-dir
// probe had already matched it. It lets callers that know the root skip
// probing, and keeps tests from reaching into detection's unexported fields.
func resolved(root string) detection {
	return detection{root: root, via: DetectConfigDir}
}

// describeSignals renders what a harness looks for, for the "not found" case.
// Without it the CLI can only name the config path, leaving users unaware that
// installing the tool is enough on its own.
func describeSignals(spec detectSpec) string {
	s := locate.Display(spec.candidates)
	bins := strings.Join(spec.binaries, " or ")
	if bins == "" {
		return s
	}
	if s == "" {
		return bins + " on PATH"
	}
	return s + ", or " + bins + " on PATH"
}
