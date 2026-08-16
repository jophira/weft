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

// projectWriteKey is the config map gating inline delivery per harness, e.g.
//
//	project_write:
//	  codex: true
//
// Only inline delivery consults it. Hook and import delivery cost either no
// tracked diff or a single line that never changes again, so gating them would
// be friction without a matching risk.
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
		wrote, err := project.DeliverImport(root, spec.Path, spec.ImportTemplate, paths)
		if err != nil {
			return nil, err
		}
		return &project.Delivered{
			Harness: name, How: spec.Delivery.String(), Path: spec.Path,
			Wrote: wrote, TrackedByGit: true,
		}, nil

	case harness.ProjectInline:
		if !inlineWriteAllowed(name) {
			return nil, nil
		}
		parts := project.PartsFor(written, spec.Inputs)
		if len(parts) == 0 {
			return nil, nil
		}
		wrote, err := project.DeliverInline(root, spec.Path, spec.Preamble, parts)
		if err != nil {
			return nil, err
		}
		return &project.Delivered{
			Harness: name, How: spec.Delivery.String(), Path: spec.Path,
			Wrote: wrote, TrackedByGit: true,
		}, nil
	}
	return nil, nil
}

// inlineWriteAllowed reports whether the user opted this harness into writing a
// tracked project file that is rewritten whenever rules change.
func inlineWriteAllowed(name string) bool {
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
	var waiting []string
	for _, h := range detected {
		spec := harness.ProjectSupportOf(h)
		if spec.Delivery == harness.ProjectInline && !inlineWriteAllowed(h.Name()) {
			waiting = append(waiting, h.Name())
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
