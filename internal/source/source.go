package source

// Structure describes the subdirectory layout within a source root.
type Structure struct {
	Commands string `yaml:"commands"         mapstructure:"commands"`
	Agents   string `yaml:"agents"           mapstructure:"agents"`
	Skills   string `yaml:"skills"           mapstructure:"skills"`
	Memory   string `yaml:"memory"           mapstructure:"memory"`
	Hooks    string `yaml:"hooks"            mapstructure:"hooks"`
	// InstructionGlob controls which files are assembled into the effective
	// CLAUDE.md for this source. A plain filename (default "CLAUDE.md") reads
	// only that root-level file. A glob like "**/*.md" walks the full tree and
	// concatenates every matching file in parent-before-child order. Managed
	// subdirectory files (commands, skills, etc.) are always excluded from
	// assembly regardless of this pattern.
	InstructionGlob string `yaml:"instruction_glob" mapstructure:"instruction_glob"`
}

// Source is a directory of AI rules backed by a git remote.
type Source struct {
	Name      string    `yaml:"name"       mapstructure:"name"`
	Root      string    `yaml:"root"       mapstructure:"root"`
	Remote    string    `yaml:"remote"     mapstructure:"remote"`
	Branch    string    `yaml:"branch"     mapstructure:"branch"`
	AutoPull  bool      `yaml:"auto_pull"  mapstructure:"auto_pull"`
	AutoPush  bool      `yaml:"auto_push"  mapstructure:"auto_push"`
	Structure Structure `yaml:"structure"  mapstructure:"structure"`
}

func DefaultStructure() Structure {
	return Structure{
		Commands:        "commands/",
		Agents:          "agents/",
		Skills:          "skills/",
		Memory:          "memory/",
		Hooks:           "hooks/",
		InstructionGlob: "CLAUDE.md",
	}
}

// Registry manages the registered set of sources.
type Registry interface {
	Add(s Source) error
	Remove(name string) error
	Get(name string) (*Source, error)
	List() ([]Source, error)
}
