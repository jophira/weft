package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/advice"
	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/project"
	"github.com/jophira/weft/internal/rules"
)

// projectWriteKey is the config map letting weft edit a project file it does not
// own, per harness, e.g.
//
//	project_write:
//	  codex: true
//
// Inline delivery always consults it, because it rewrites the file whenever
// rules change. Import delivery consults it only when the instruction file
// already exists and weft did not create it: a file weft made is excluded from
// git and needs no permission, but editing a file the repository's owners wrote
// does. Hook delivery never consults it, because its settings file is excluded.
const projectWriteKey = "project_write"

// syncProject delivers the project bundle for one repository.
//
// Returns what each harness received, so callers can report it or stay quiet.
// Errors from one harness do not stop the others: a repository where Codex
// delivery fails should still get its Claude Code hook.
func syncProject(root string, harnesses []harness.Harness, out io.Writer) ([]project.Delivered, error) {
	// Exclude weft's state directory and anything hook delivery writes. The
	// latter matters because a settings file being "conventionally gitignored"
	// is a convention, not a rule: a fresh repository ignores nothing.
	if _, err := project.EnsureExcluded(root, hookPaths(harnesses)...); err != nil {
		// Not fatal, but it does mean the state directory would show up in git
		// status, which is the one outcome this design promises to avoid.
		advice.Add(advice.Advice{
			Code:     advice.CodeProjectExcludeFailed,
			Severity: advice.Warn,
			Message:  fmt.Sprintf("could not exclude %s from git: %v", project.StateDirName, err),
			Fix:      "add /.weft/ to .git/info/exclude by hand",
		})
	}

	parts, err := projectParts(root, harnesses)
	if err != nil {
		return nil, err
	}
	written, err := project.WriteParts(root, parts)
	if err != nil {
		return nil, err
	}

	var delivered []project.Delivered
	for _, h := range harnesses {
		spec := harness.ProjectSupportOf(h)
		d, dErr := deliverTo(root, h.Name(), spec, written)
		if dErr != nil {
			slog.Warn("project.sync", slog.String("harness", h.Name()), slog.String("error", dErr.Error()))
			fmt.Fprintf(out, "  ! %s: %v\n", h.Name(), dErr)
			continue
		}
		if d == nil {
			continue
		}
		delivered = append(delivered, *d)
	}

	slog.Info("project.sync",
		slog.String("root", root),
		slog.Int("parts", len(written)),
		slog.Int("harnesses", len(delivered)),
	)
	return delivered, nil
}

// deliverTo applies one harness's delivery, or returns nil when there is
// nothing to do for it.
func deliverTo(root, name string, spec harness.ProjectSpec, written []project.WrittenPart) (*project.Delivered, error) {
	switch spec.Delivery {
	case harness.ProjectHook:
		wrote, err := project.DeliverHook(root, spec.Path)
		if err != nil {
			return nil, err
		}
		return &project.Delivered{Harness: name, How: spec.Delivery.String(), Path: spec.Path, Wrote: wrote}, nil

	case harness.ProjectImport:
		paths := project.ImportPathsFor(written, spec.Inputs)
		if len(paths) == 0 {
			return nil, nil
		}
		owned := project.Owned(root, spec.Path)
		if !owned && !projectWriteAllowed(name) {
			// The watcher reaches here without ever calling reportDelivery, so
			// the log is the only trace that a file was left alone.
			slog.Info("project.sync.gated", slog.String("harness", name), slog.String("path", spec.Path))
			return nil, nil
		}
		wrote, created, err := project.DeliverImport(root, spec.Path, spec.ImportTemplate, paths)
		if err != nil {
			return nil, err
		}
		excludeCreated(root, spec.Path, created)
		return &project.Delivered{
			Harness: name, How: spec.Delivery.String(), Path: spec.Path,
			Wrote: wrote, TrackedByGit: !owned,
		}, nil

	case harness.ProjectInline:
		if !projectWriteAllowed(name) {
			return nil, nil
		}
		parts := project.PartsFor(written, spec.Inputs)
		if len(parts) == 0 {
			return nil, nil
		}
		owned := project.Owned(root, spec.Path)
		wrote, created, err := project.DeliverInline(root, spec.Path, spec.Preamble, parts)
		if err != nil {
			return nil, err
		}
		excludeCreated(root, spec.Path, created)
		return &project.Delivered{
			Harness: name, How: spec.Delivery.String(), Path: spec.Path,
			Wrote: wrote, TrackedByGit: !owned,
		}, nil
	}
	return nil, nil
}

// excludeCreated keeps a file weft has just created out of git status, which is
// the whole point of creating it: an untracked GEMINI.md in somebody else's
// repository is a change they never asked for.
//
// The exclusion is written only at creation, never on later syncs. A line the
// user removed by hand is a decision, and restoring it would undo it. The file
// then counts as not owned, so the next sync asks for project-write instead.
func excludeCreated(root, path string, created bool) {
	if !created {
		return
	}
	if _, err := project.EnsureExcluded(root, path); err != nil {
		// The file stays: deleting it would lose what was just delivered, and
		// doctor lists it as an orphan so it does not go unnoticed.
		advice.Add(advice.Advice{
			Code:     advice.CodeProjectExcludeFailed,
			Severity: advice.Warn,
			Message:  fmt.Sprintf("could not exclude %s from git: %v", path, err),
			Fix:      fmt.Sprintf("add /%s to .git/info/exclude by hand, or delete it", path),
		})
	}
}

// projectWriteAllowed reports whether the user opted this harness into editing a
// project file weft did not create. One switch with one meaning, for inline and
// import delivery alike.
func projectWriteAllowed(name string) bool {
	return viper.GetStringMapString(projectWriteKey)[name] == "true" ||
		viper.GetBool(projectWriteKey+"."+name)
}

// projectParts assembles the bundle: rules the resolver selected for this
// repository, then the repository's own instruction files.
//
// Inputs come last, so they win. Priority in weft is "last emitted wins", and
// the repository is the most specific context there is: a rule written in the
// repo should beat a general one from a global source.
func projectParts(root string, harnesses []harness.Harness) ([]project.Part, error) {
	var parts []project.Part

	bundle, err := projectRuleBundle(root)
	if err != nil {
		return nil, err
	}
	if bundle != "" {
		parts = append(parts, project.Part{Name: "weft-rules", Origin: project.OriginRules, Content: bundle})
	}

	exclude := deliveryPaths(harnesses)
	inputs, err := project.ReadInputs(root, harness.ProjectInputs(harnesses), exclude)
	if err != nil {
		return nil, err
	}
	for _, in := range inputs {
		parts = append(parts, project.Part{Name: in.Rel, Origin: project.OriginInput, Content: in.Content})
	}
	return parts, nil
}

// hookPaths lists the repo-relative settings files hook delivery writes, which
// must be excluded from git for that delivery to cost nothing.
func hookPaths(harnesses []harness.Harness) []string {
	var out []string
	for _, h := range harnesses {
		if spec := harness.ProjectSupportOf(h); spec.Delivery == harness.ProjectHook && spec.Path != "" {
			out = append(out, spec.Path)
		}
	}
	sort.Strings(out)
	return out
}

// deliveryPaths lists the repo-relative files weft writes, so fan-in never reads
// back weft's own output as if the user had written it.
func deliveryPaths(harnesses []harness.Harness) []string {
	var out []string
	for _, h := range harnesses {
		if spec := harness.ProjectSupportOf(h); spec.Delivery.TracksGit() {
			out = append(out, spec.Path)
		}
	}
	sort.Strings(out)
	return out
}

// projectRuleBundle resolves the active profile's rules against the repository,
// returning the assembled text.
//
// This is the same resolution the session hook performs, reused so import and
// inline delivery carry exactly what a hook-capable harness would receive.
func projectRuleBundle(root string) (string, error) {
	roots, _, err := resolveRootSpecs()
	if err != nil {
		// No active profile is a legitimate state: the repository's own files are
		// still worth delivering.
		return "", nil
	}
	// Cache enabled: project sync runs on every source change across every
	// registered repository, so a forced rebuild each time would be felt.
	ress, err := resolveAcrossRoots(root, roots, rules.CacheOptions{})
	if err != nil {
		return "", nil
	}
	return layerBundles(ress), nil
}

// projectInputBundle renders the repository's own instruction files as a
// bundle section, for the resolver's stdout.
//
// Returns empty when the repository has none, which is a normal state and not
// an error: the resolver is a total function and a hook must never fail over
// the absence of an optional input.
func projectInputBundle(root string) (string, error) {
	detected := detectedHarnesses()
	if len(detected) == 0 {
		return "", nil
	}
	inputs, err := project.ReadInputs(root, harness.ProjectInputs(detected), deliveryPaths(detected))
	if err != nil {
		return "", err
	}
	if len(inputs) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("# Project instructions\n")
	b.WriteString("\nFrom this repository's own instruction files.\n")
	for _, in := range inputs {
		fmt.Fprintf(&b, "\n<!-- weft:project:begin path=%q -->\n", in.Rel)
		b.WriteString(in.Content)
		fmt.Fprintf(&b, "\n<!-- weft:project:end path=%q -->\n", in.Rel)
	}
	return b.String(), nil
}

// detectedHarnesses returns the installed harnesses, which is the set project
// delivery considers. Unlike the global plane there is no profile target list:
// a repository is worked in with whatever is installed, and a harness that
// cannot take project rules is reported by reportDelivery rather than filtered
// out silently.
func detectedHarnesses() []harness.Harness {
	var out []harness.Harness
	for _, h := range harness.Instances() {
		if h.Detect() {
			out = append(out, h)
		}
	}
	return out
}

// reportDelivery prints what each harness got, and raises hints for the
// harnesses that got nothing.
func reportDelivery(out io.Writer, root string, delivered []project.Delivered, detected []harness.Harness) {
	if len(delivered) == 0 {
		fmt.Fprintln(out, "Nothing delivered: no detected harness can take project rules here.")
	}
	for _, d := range delivered {
		mark := "·" // unchanged
		if d.Wrote {
			mark = "✓"
		}
		note := ""
		if d.TrackedByGit {
			note = "  (tracked by git)"
		}
		fmt.Fprintf(out, "  %s %-12s %-7s %s%s\n", mark, d.Harness, d.How, d.Path, note)
	}

	// Name the harnesses in use here that inline delivery would reach, so the
	// choice to accept a recurring diff is offered rather than assumed.
	var waiting, foreign []string
	for _, h := range detected {
		spec := harness.ProjectSupportOf(h)
		if spec.Delivery == harness.ProjectInline && !projectWriteAllowed(h.Name()) {
			waiting = append(waiting, h.Name())
		}
		if spec.Delivery == harness.ProjectImport && !projectWriteAllowed(h.Name()) && !project.Owned(root, spec.Path) {
			foreign = append(foreign, h.Name())
		}
	}
	if len(waiting) > 0 {
		sort.Strings(waiting)
		verb := "needs"
		if len(waiting) > 1 {
			verb = "need"
		}
		advice.Add(advice.Advice{
			Code:     advice.CodeProjectWriteOff,
			Severity: advice.Info,
			Message: fmt.Sprintf("%s %s a tracked file written to receive project rules here, currently off",
				strings.Join(waiting, ", "), verb),
			Fix: fmt.Sprintf("enable per harness: weft config set project-write %s true", waiting[0]),
		})
	}

	// A separate advice from the inline one, under its own code: the cause is
	// different (a file that already exists and is not weft's), so is the remedy,
	// and the bus keeps only the first advice per code.
	if len(foreign) > 0 {
		sort.Strings(foreign)
		verb := "needs"
		if len(foreign) > 1 {
			verb = "need"
		}
		advice.Add(advice.Advice{
			Code:     advice.CodeProjectFileForeign,
			Severity: advice.Info,
			Message: fmt.Sprintf("%s %s to edit a file weft did not create, currently off",
				strings.Join(foreign, ", "), verb),
			Fix: fmt.Sprintf("if weft made it, run 'weft doctor --fix'; to edit the repo's own file: weft config set project-write %s true", foreign[0]),
		})
	}

	fmt.Fprintf(out, "\n  state: %s\n", project.InstructionsDir(root))
}

// syncActiveProjects delivers to every registered project that is enabled and
// still fresh. Used by the watcher after a source change.
func syncActiveProjects(harnesses []harness.Harness, out io.Writer) {
	cfgDir := configDir()
	if cfgDir == "" {
		return
	}
	reg, err := project.Load(cfgDir)
	if err != nil {
		return
	}
	active := reg.Active(time.Now().UTC(), projectMaxAge())
	for _, p := range active {
		if _, err := os.Stat(p.Root); err != nil {
			continue // pruned on the next registration
		}
		if _, err := syncProject(p.Root, harnesses, out); err != nil {
			slog.Warn("project.sync", slog.String("root", p.Root), slog.String("error", err.Error()))
		}
	}
	if len(active) > 0 {
		slog.Info("project.sync.batch", slog.Int("projects", len(active)))
	}
}
