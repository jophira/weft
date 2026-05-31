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

// Profile is a named combination of sources with a merge strategy.
type Profile struct {
	Name         string   `yaml:"name"          mapstructure:"name"`
	Sources      []string `yaml:"sources"       mapstructure:"sources"`
	Overlay      Overlay  `yaml:"overlay"       mapstructure:"overlay"`
	ActiveTarget string   `yaml:"active_target" mapstructure:"active_target"`
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
