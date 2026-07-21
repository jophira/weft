package profile

// Overlay determines how conflicts are resolved when merging sources.
type Overlay string

const (
	// OverlayCascade keeps base entries not in overlay, overlay wins on conflict.
	OverlayCascade Overlay = "cascade"
	// OverlayMerge appends overlay content to base (line-level).
	OverlayMerge Overlay = "merge"
	// OverlayLastWins overlay always replaces base entirely.
	OverlayLastWins Overlay = "last-wins"
)

// WriteBack configures which source receives edits when a merged target file
// is changed outside weft. Single-source files always write back to their sole
// owning source and never consult this config.
type WriteBack struct {
	Default   string            `yaml:"default"   mapstructure:"default"`
	Overrides map[string]string `yaml:"overrides" mapstructure:"overrides"`
}

// HarnessSync restricts which file classes weft projects to a given harness,
// keyed by harness name:
//
//	harness_sync:
//	  codex:  [instructions, commands]
//	  cursor: []
//
// A harness with no entry projects every class it natively supports, which is
// weft's behaviour without this config. An explicit empty list means "project
// nothing", so silence and emptiness are deliberately different: omitting a key
// must not be a way to accidentally disable a harness.
//
// Class names are validated where the harness package is available; this package
// stores them as plain strings to stay independent of it.
type HarnessSync map[string][]string

// ClassesFor returns the configured class list for a harness and whether one was
// configured at all. The second return distinguishes an absent key (use the
// harness default) from an explicit empty list (project nothing).
//
// cf. Java: Optional<List<String>> — Go's comma-ok idiom carries the same
// "present but empty" vs "absent" distinction.
func (h HarnessSync) ClassesFor(harness string) ([]string, bool) {
	if h == nil {
		return nil, false
	}
	classes, ok := h[harness]
	return classes, ok
}

// Profile is a named combination of sources with a merge strategy.
type Profile struct {
	Name         string      `yaml:"name"          mapstructure:"name"`
	Sources      []string    `yaml:"sources"       mapstructure:"sources"`
	Overlay      Overlay     `yaml:"overlay"       mapstructure:"overlay"`
	ActiveTarget string      `yaml:"active_target,omitempty" mapstructure:"active_target"`
	Targets      []string    `yaml:"targets,omitempty"      mapstructure:"targets"`
	WriteBack    WriteBack   `yaml:"write_back"    mapstructure:"write_back"`
	HarnessSync  HarnessSync `yaml:"harness_sync,omitempty" mapstructure:"harness_sync"`
}

// ResolvedTargets returns the effective target list for the profile.
// It prefers the Targets field when non-empty; otherwise it falls back to
// wrapping the legacy ActiveTarget in a single-element slice. Returns nil when
// neither field is set.
func (p *Profile) ResolvedTargets() []string {
	if len(p.Targets) > 0 {
		return p.Targets
	}
	if p.ActiveTarget != "" {
		return []string{p.ActiveTarget}
	}
	return nil
}

// Manager handles profile persistence and activation.
type Manager interface {
	Create(p Profile) error
	Delete(name string) error
	Get(name string) (*Profile, error)
	List() ([]Profile, error)
	Active() (*Profile, error)
	Activate(name string) error
}
