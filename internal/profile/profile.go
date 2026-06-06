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

// Profile is a named combination of sources with a merge strategy.
type Profile struct {
	Name         string    `yaml:"name"          mapstructure:"name"`
	Sources      []string  `yaml:"sources"       mapstructure:"sources"`
	Overlay      Overlay   `yaml:"overlay"       mapstructure:"overlay"`
	ActiveTarget string    `yaml:"active_target" mapstructure:"active_target"`
	WriteBack    WriteBack `yaml:"write_back"    mapstructure:"write_back"`
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
