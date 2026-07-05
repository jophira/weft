package source

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jophira/weft/internal/locate"
)

// SortByPriority orders sources in place by ascending Priority so that
// higher-priority sources are emitted later and win on conflict under the
// cascade/last-wins overlay. The sort is stable: sources sharing a priority keep
// their incoming relative order, so the all-zero default leaves order untouched
// (backward compatible with the previous profile-order behaviour).
func SortByPriority(srcs []Source) {
	slices.SortStableFunc(srcs, func(a, b Source) int { return a.Priority - b.Priority })
}

// scaffoldInstructionFile is the canonical instruction filename created in a
// flat-mode source root that has none.
const scaffoldInstructionFile = "CLAUDE.md"

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
	// InstructionExclude lists root-relative path prefixes (directories or
	// files) excluded from instruction assembly, in addition to the always-
	// excluded managed dirs. This lets a mixed-content source inline only a
	// subset with a broad glob — e.g. "**/*.md" with
	// InstructionExclude ["java/", "tickets/", "docs/"] assembles the always-on
	// rules while leaving language/ticket/doc trees on-disk and on-demand.
	// Configure via --instruction-exclude on `weft source add`.
	InstructionExclude []string `yaml:"instruction_exclude" mapstructure:"instruction_exclude"`
}

// isZero reports whether s is a zero-value Structure (no fields set).
// Used in place of == comparison since Structure contains a slice.
func (s Structure) isZero() bool {
	return s.Commands == "" && s.Agents == "" && s.Skills == "" &&
		s.Memory == "" && s.Hooks == "" && s.Projects == "" &&
		s.InstructionGlob == "" && len(s.ProjectDirNames) == 0 &&
		len(s.InstructionExclude) == 0
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
	Name string `yaml:"name"       mapstructure:"name"`
	Root string `yaml:"root"       mapstructure:"root"`
	// Priority orders a source within a profile's layered assembly. Higher
	// numbers are more important and are emitted *later* so they take precedence
	// on conflict (consistent with the cascade/last-wins overlay semantics and
	// LLM recency bias). Unset (0) is the lowest priority; sources sharing a
	// priority keep their relative order from the profile's source list.
	// cf. Java: a comparator key — Go sorts with a stable sort.SliceStable.
	Priority  int       `yaml:"priority"   mapstructure:"priority"`
	Remote    string    `yaml:"remote"     mapstructure:"remote"`
	Branch    string    `yaml:"branch"     mapstructure:"branch"`
	AutoPull  bool      `yaml:"auto_pull"  mapstructure:"auto_pull"`
	AutoPush  bool      `yaml:"auto_push"  mapstructure:"auto_push"`
	Structure Structure `yaml:"structure"  mapstructure:"structure"`
}

// EnsureInstructionFile creates a minimal CLAUDE.md in the source root when the
// source uses flat instruction mode (InstructionGlob empty or "CLAUDE.md") and
// no instruction file exists yet. It is a no-op for hierarchical sources (a
// glob other than the canonical name) and when the file already exists.
// Returns true when a file was created.
//
// cf. Java: a guarded "create if absent" — Go has no Files.createFile + EXCL
// helper, so we Stat then Write.
func (s Source) EnsureInstructionFile() (bool, error) {
	if glob := s.Structure.InstructionGlob; glob != "" && glob != scaffoldInstructionFile {
		return false, nil // hierarchical source — author manages their own tree
	}
	root := locate.ExpandHome(s.Root)
	path := filepath.Join(root, scaffoldInstructionFile)
	switch _, err := os.Stat(path); {
	case err == nil:
		return false, nil // already present
	case !os.IsNotExist(err):
		return false, fmt.Errorf("checking %s: %w", path, err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return false, fmt.Errorf("creating source root %s: %w", root, err)
	}
	content := fmt.Sprintf("# %s rules\n\n<!-- weft: add this source's instructions here -->\n", s.Name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // path derived from registered source root, not user input
		return false, fmt.Errorf("scaffolding %s: %w", path, err)
	}
	return true, nil
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

// InstructionExcludes returns every root-relative prefix excluded from
// instruction assembly: the always-excluded managed/project dirs (AllDirs)
// plus any user-configured InstructionExclude entries. Use this as the exclude
// set when assembling instruction content so a mixed-content source can inline
// only a subset with a broad glob.
func (s Structure) InstructionExcludes() []string {
	return append(s.AllDirs(), cleanDirs(s.InstructionExclude...)...)
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
