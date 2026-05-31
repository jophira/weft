package harness

// Harness is an AI coding tool (Claude Code, Cursor, Warp, etc.)
// that consumes a merged rule profile.
type Harness interface {
	// Name returns the harness identifier used in CLI commands.
	Name() string
	// Detect reports whether this harness is installed on the current machine.
	Detect() bool
	// Apply writes the merged profile into the harness config location.
	Apply(mergedRoot string) error
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
