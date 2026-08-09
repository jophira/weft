package harness

// Harness is an AI coding tool (Claude Code, Cursor, Warp, etc.)
// that consumes a merged rule profile.
type Harness interface {
	// Name returns the harness identifier used in CLI commands.
	Name() string
	// Detect reports whether this harness is installed on the current machine.
	Detect() bool
	// Apply writes the merged profile into the harness config location.
	// ctx carries the profile name and config dir for manifest tracking.
	Apply(stagedRoot string, ctx ApplyCtx) error
}

// ConfigPather is an optional extension of Harness for adapters whose config
// root cannot be known until runtime (e.g. varies by platform or Warp version).
// When implemented, the returned path is used in place of the static Known.ConfigPath.
type ConfigPather interface {
	ConfigPath() string
}

// DetectReporter is an optional extension of Harness that reports which signal
// satisfied the last Detect call. Adapters get it for free by embedding
// detection; the CLI uses it to name the evidence it actually matched instead
// of printing a config path it never checked.
type DetectReporter interface {
	DetectedVia() (DetectVia, string)
}

// signaller is implemented by adapters that declare their detection signals,
// letting the CLI describe what it looked for when nothing was found.
type signaller interface {
	detectSignals() detectSpec
}

// DetectSignals renders the signals a harness is identified by (config roots and
// binary), for adapters that declare them. Returns "" for any that do not.
func DetectSignals(h Harness) string {
	if s, ok := h.(signaller); ok {
		return describeSignals(s.detectSignals())
	}
	return ""
}

// Registry holds all known harness adapters.
type Registry struct {
	harnesses []Harness
}

func NewRegistry(harnesses ...Harness) *Registry {
	return &Registry{harnesses: harnesses}
}

func (r *Registry) Detect() []Harness {
	var found []Harness
	for _, h := range r.harnesses {
		if h.Detect() {
			found = append(found, h)
		}
	}
	return found
}

func (r *Registry) Get(name string) (Harness, bool) {
	for _, h := range r.harnesses {
		if h.Name() == name {
			return h, true
		}
	}
	return nil, false
}
