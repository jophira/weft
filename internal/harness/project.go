package harness

import "path/filepath"

// ProjectDelivery is how a harness receives project-scoped rules, ranked by how
// much of a git diff it costs. Weft picks the highest level a harness supports
// and the config allows.
//
// The ranking is the whole design. Repo-local rule state lives in <repo>/.weft
// and is kept out of git via .git/info/exclude, so the only question left is how
// each harness is pointed at it, and the answer differs by capability.
type ProjectDelivery int

const (
	// ProjectNone: weft has no reliable way to deliver project rules to this
	// harness. Reported, never guessed. Writing to a path the harness ignores is
	// the bug ADR 0004's class model exists to prevent, and the project plane
	// inherits that rule.
	ProjectNone ProjectDelivery = iota

	// ProjectHook: the harness runs a command at session start and folds its
	// stdout into context, and the settings file carrying that hook can be kept
	// out of git. Costs no tracked diff at all, so it wins wherever available.
	ProjectHook

	// ProjectImport: the harness resolves an include directive itself, so one
	// line in the tracked instruction file is enough. The line is written once
	// and never changes, because everything that varies lives in the file it
	// points at.
	ProjectImport

	// ProjectInline: the harness has no include directive, so the content must
	// be written into the tracked file and rewritten whenever rules change.
	// Off by default: a recurring diff in a shared repository is a decision for
	// the user, not for weft.
	//
	// A pointer line is deliberately not offered as a cheaper substitute here.
	// Without an include directive it is a request the model may ignore, and a
	// rule that applies on a coin flip is worse than one that never applies,
	// because there is no way to tell from the outside which happened. ADR 0004's
	// Advertise draws the same line: model-discretion pointers are acceptable for
	// optional content, not for rules that must always apply.
	ProjectInline
)

func (d ProjectDelivery) String() string {
	switch d {
	case ProjectHook:
		return "hook"
	case ProjectImport:
		return "import"
	case ProjectInline:
		return "inline"
	}
	return "none"
}

// TracksGit reports whether this delivery writes a file normally kept in git.
// Used to decide what needs the user's explicit go-ahead.
func (d ProjectDelivery) TracksGit() bool {
	return d == ProjectImport || d == ProjectInline
}

// ProjectSpec describes one harness's project-scoped behaviour.
type ProjectSpec struct {
	// Delivery is how weft reaches this harness inside a repository.
	Delivery ProjectDelivery
	// Path is repo-relative: the settings file gaining a hook for ProjectHook,
	// or the instruction file written for ProjectImport and ProjectInline.
	Path string
	// ImportTemplate renders one import line for ProjectImport, with "{path}"
	// replaced. Empty means the package default.
	ImportTemplate string
	// Preamble is seeded outside the managed block when weft first creates the
	// file, for formats that need a header to be read at all (Cursor's .mdc
	// frontmatter).
	Preamble string
	// Inputs are repo-relative files (plain paths or globs) weft reads as
	// project-scoped input. Reading is never gated: a tracked file weft will
	// never write is still a legitimate source of rules for that repository.
	Inputs []string
}

// ProjectAware is an optional Harness extension declaring project behaviour.
// Harnesses that do not implement it have no project support, which is reported
// rather than approximated.
type ProjectAware interface {
	ProjectSpec() ProjectSpec
}

// ProjectSupportOf resolves a harness's project behaviour.
func ProjectSupportOf(h Harness) ProjectSpec {
	if pa, ok := h.(ProjectAware); ok {
		return pa.ProjectSpec()
	}
	return ProjectSpec{Delivery: ProjectNone}
}

// ProjectInputs returns the union of repo-relative input patterns declared by
// the given harnesses, deduplicated and order-stable.
//
// A union rather than a per-harness read: a rule someone wrote in CLAUDE.md
// should reach Codex in that repository too, which is the whole point of weft
// having a single source of truth.
func ProjectInputs(harnesses []Harness) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range harnesses {
		for _, in := range ProjectSupportOf(h).Inputs {
			clean := filepath.ToSlash(filepath.Clean(in))
			if clean == "" || seen[clean] {
				continue
			}
			seen[clean] = true
			out = append(out, clean)
		}
	}
	return out
}
