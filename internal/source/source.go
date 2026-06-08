package source

import "strings"

// Structure describes the subdirectory layout within a source root.
type Structure struct {
	Commands string `yaml:"commands"         mapstructure:"commands"`
	Agents   string `yaml:"agents"           mapstructure:"agents"`
	Skills   string `yaml:"skills"           mapstructure:"skills"`
	Memory   string `yaml:"memory"           mapstructure:"memory"`
	Hooks    string `yaml:"hooks"            mapstructure:"hooks"`
	// Projects is the optional subdirectory containing per-project rule files.
	// When set, weft expands the <!-- weft:projects --> placeholder in the
	// assembled CLAUDE.md with a generated snippet listing the source's project
	// file paths. The directory is never merged into the harness target.
	Projects string `yaml:"projects"         mapstructure:"projects"`
	// ProjectDirNames is the list of directory base-names that weft treats as
	// project-rules roots when auto-discovering project files. Any directory
	// found anywhere in the source tree whose base name matches one of these
	// names is treated as a project root: its contents are enumerated
	// recursively and referenced in the assembled CLAUDE.md snippet.
	//
	// Defaults to ["projects", "project-rules"] when nil or empty.
	// Configure via --project-dir-names on `weft source add`, or by setting
	// project_dir_names in the source YAML.
	ProjectDirNames []string `yaml:"project_dir_names" mapstructure:"project_dir_names"`
	// InstructionGlob controls which files are assembled into the effective
	// CLAUDE.md for this source. A plain filename (default "CLAUDE.md") reads
	// only that root-level file. A glob like "**/*.md" walks the full tree and
	// concatenates every matching file in parent-before-child order. Managed
	// subdirectory files (commands, skills, etc.) are always excluded from
	// assembly regardless of this pattern.
	InstructionGlob string `yaml:"instruction_glob" mapstructure:"instruction_glob"`
}

// isZero reports whether s is a zero-value Structure (no fields set).
// Used in place of == comparison since Structure contains a slice.
func (s Structure) isZero() bool {
	return s.Commands == "" && s.Agents == "" && s.Skills == "" &&
		s.Memory == "" && s.Hooks == "" && s.Projects == "" &&
		s.InstructionGlob == "" && len(s.ProjectDirNames) == 0
}

// defaultProjectDirNames are the directory names weft searches for when no
// explicit project_dir_names are configured.
var defaultProjectDirNames = []string{"projects", "project-rules"}

// EffectiveProjectDirNames returns the configured project dir names, or the
// built-in defaults (["projects", "project-rules"]) when none are set.
func (s Structure) EffectiveProjectDirNames() []string {
	if len(s.ProjectDirNames) > 0 {
		return s.ProjectDirNames
	}
	return defaultProjectDirNames
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

// ManagedDirs returns the non-empty, trimmed names of the managed
// subdirectories (Commands, Agents, Skills, Memory, Hooks). Projects is
// excluded because it is a generated output directory, not a merge input.
func (s Structure) ManagedDirs() []string {
	return cleanDirs(s.Commands, s.Agents, s.Skills, s.Memory, s.Hooks)
}

// AllDirs returns ManagedDirs plus Projects (when set). Use this when
// excluding directories from instruction assembly, where project files must
// also be omitted.
func (s Structure) AllDirs() []string {
	return cleanDirs(s.Commands, s.Agents, s.Skills, s.Memory, s.Hooks, s.Projects)
}

// cleanDirs trims whitespace and trailing path separators from each name,
// returning only non-empty results.
func cleanDirs(dirs ...string) []string {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d = strings.TrimRight(strings.TrimSpace(d), "/\\"); d != "" {
			out = append(out, d)
		}
	}
	return out
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
