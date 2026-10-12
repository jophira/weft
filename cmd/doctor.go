package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/instruction"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/pathlint"
	"github.com/jophira/weft/internal/project"
	"github.com/spf13/cobra"
)

var (
	doctorFix bool
	doctorAll bool
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check system health and discover AI rule folders",
	Long: `Check system health, discover AI rule folders, and lint source path
references. Reports hardcoded, stale, broken, and dead path references in your
sources. Pass --fix to rewrite the healable ones to the portable {{weft.root}}
and {{weft.source:NAME}} anchors, and to delete project files that weft created
before it excluded them from git and that hold nothing but its own block.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		runDoctor(os.Stdout)
		// Not part of runDoctor: that output is also the body of 'weft bug-report',
		// which people paste into public issues, and a list of paths inside
		// their private repositories does not belong there.
		orphans := reportProjectOrphans(os.Stdout)
		if !doctorFix {
			return nil
		}
		return errors.Join(removeProjectOrphans(os.Stdout, orphans), fixPaths(os.Stdout))
	},
}

// runDoctor writes the health check output to w. Shared by doctorCmd and bug-report.
func runDoctor(w io.Writer) {
	fmt.Fprintln(w, "Jophira Health Check")
	fmt.Fprintln(w, "────────────────────")

	cfgDir := configDir()
	if _, err := os.Stat(cfgDir); err == nil {
		fmt.Fprintf(w, "  ✓ Config dir: %s\n", cfgDir)
	} else {
		fmt.Fprintf(w, "  ✗ Config dir missing: %s\n", cfgDir)
	}

	scanHarnesses(w)

	active := activeProfileName()
	if active != "" {
		if pm, err := newProfileManager(); err == nil {
			if p, err := pm.Get(active); err == nil {
				targets := p.ResolvedTargets()
				if len(targets) > 0 {
					fmt.Fprintf(w, "\nActive profile %q — target health:\n", active)
					hReg := harness.NewRegistry(harness.Instances()...)
					for _, t := range targets {
						h, ok := hReg.Get(t)
						if !ok {
							fmt.Fprintf(w, "  ✗ %s: unknown harness\n", t)
							continue
						}
						if h.Detect() {
							fmt.Fprintf(w, "  ✓ %s: detected\n", t)
						} else {
							fmt.Fprintf(w, "  – %s: not detected\n", t)
						}
					}
				}
			}
		}
	} else {
		if pm, err := newProfileManager(); err == nil {
			profiles, _ := pm.List()
			if len(profiles) > 0 {
				var names []string
				for _, p := range profiles {
					names = append(names, p.Name)
				}
				fmt.Fprintf(w, "\nNo active profile. Available: %s\n", strings.Join(names, ", "))
				fmt.Fprintf(w, "Activate one with: weft profile use <name>\n")
			}
		}
	}

	reportProfileIntegrity(w)
	reportRuleHealth(w)
	reportPaths(w)
}

// projectTarget is one delivery-target file in one registered repository.
type projectTarget struct {
	root string
	rel  string
	spec harness.ProjectSpec
}

func (t projectTarget) abs() string { return filepath.Join(t.root, filepath.FromSlash(t.rel)) }

// projectTargets lists the delivery-target files that exist in each root.
//
// Every harness is considered, not only detected ones: the leftovers of a
// harness that has since been uninstalled are exactly what this looks for.
func projectTargets(roots []string, harnesses []harness.Harness) []projectTarget {
	var out []projectTarget
	seen := map[string]bool{}
	for _, root := range roots {
		for _, h := range harnesses {
			spec := harness.ProjectSupportOf(h)
			if !spec.Delivery.TracksGit() || spec.Path == "" {
				continue
			}
			t := projectTarget{root: root, rel: spec.Path, spec: spec}
			if seen[t.abs()] {
				continue // two harnesses sharing a target, e.g. AGENTS.md
			}
			if _, err := os.Stat(t.abs()); err == nil {
				seen[t.abs()] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// orphanProjectFiles returns the absolute paths of delivery targets that git
// does not track, that info/exclude does not list, and that hold weft's block
// and nothing else.
//
// Those are what project sync left behind before it learned to exclude the
// files it creates. An ignored one counts too: a repository's own .gitignore
// can hide such a file, and sync still treats it as foreign. A file with no
// block, or anything besides it, is somebody's; an excluded or tracked one is
// live. None of those are listed.
func orphanProjectFiles(roots []string, harnesses []harness.Harness) []string {
	var out []string
	for _, t := range projectTargets(roots, harnesses) {
		data, err := os.ReadFile(t.abs())
		if err != nil {
			continue
		}
		if _, found := instruction.Extract(data); !found {
			continue // weft never wrote this file
		}
		rest := string(instruction.Strip(data))
		if t.spec.Preamble != "" {
			rest = strings.TrimPrefix(rest, t.spec.Preamble)
		}
		if strings.TrimSpace(rest) != "" {
			continue
		}
		if project.IsExcluded(t.root, t.rel) {
			continue // weft's live delivery target
		}
		// -uall so a file inside an untracked directory is reported as itself;
		// --ignored so one a .gitignore hides is reported as "!!".
		status, err := exec.Command("git", "-C", t.root, "status", "--porcelain", "--ignored", "-uall", "--", t.rel).Output() //nolint:gosec // root is a registered repository, rel a harness spec
		if code := string(status); err != nil || (!strings.HasPrefix(code, "??") && !strings.HasPrefix(code, "!!")) {
			continue
		}
		out = append(out, t.abs())
	}
	return out
}

// trackedExcludedFiles returns delivery targets that info/exclude lists but git
// tracks. Weft treats such a file as the repository's, so sync stops updating
// it, and the exclude line is what hides that from git status.
func trackedExcludedFiles(roots []string, harnesses []harness.Harness) []string {
	var out []string
	for _, t := range projectTargets(roots, harnesses) {
		if !project.IsExcluded(t.root, t.rel) {
			continue
		}
		// Asked directly rather than through project.Owned, which also returns
		// false when git fails, and that is not the same diagnosis.
		if exec.Command("git", "-C", t.root, "ls-files", "--error-unmatch", "--", t.rel).Run() == nil { //nolint:gosec // root is a registered repository, rel a harness spec
			out = append(out, t.abs())
		}
	}
	return out
}

// reportProjectOrphans lists project files weft left untracked in registered
// repositories, and returns them for --fix to delete.
func reportProjectOrphans(w io.Writer) []string {
	cfgDir := configDir()
	if cfgDir == "" {
		return nil
	}
	reg, err := project.Load(cfgDir)
	if err != nil || len(reg.Projects) == 0 {
		return nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		fmt.Fprintln(w, "\nProject files: git not found, skipping the check for leftover files")
		return nil
	}
	roots := make([]string, 0, len(reg.Projects))
	for _, p := range reg.Projects {
		roots = append(roots, p.Root)
	}
	hs := harness.Instances()

	orphans := orphanProjectFiles(roots, hs)
	if len(orphans) > 0 {
		fmt.Fprintf(w, "\nProject files weft left untracked (%d):\n", len(orphans))
		for _, o := range orphans {
			fmt.Fprintf(w, "  ✗ %s\n", o)
		}
		if !doctorFix {
			fmt.Fprintln(w, "  Run 'weft doctor --fix' to delete them; the next 'weft project sync' recreates each one and keeps it out of git.")
		}
	}
	if tracked := trackedExcludedFiles(roots, hs); len(tracked) > 0 {
		fmt.Fprintf(w, "\nProject files git tracks but .git/info/exclude also lists (%d). Weft no longer updates them:\n", len(tracked))
		for _, tf := range tracked {
			fmt.Fprintf(w, "  • %s\n", tf)
		}
		fmt.Fprintln(w, "  Remove the line from .git/info/exclude, or 'weft config set project-write <harness> true' to let weft edit the file.")
	}
	return orphans
}

// removeProjectOrphans deletes the files reportProjectOrphans listed. --fix is
// the confirmation, as it is for the path heals.
func removeProjectOrphans(w io.Writer, orphans []string) error {
	var errs []error
	removed := 0
	for _, o := range orphans {
		if err := os.Remove(o); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	if len(orphans) > 0 {
		fmt.Fprintf(w, "\n✓ Removed %d project file(s) weft had left untracked.\n", removed)
	}
	return errors.Join(errs...)
}

// reportProfileIntegrity flags profiles that reference a source name which is
// not registered — the silent-orphan failure a source rename/removal causes —
// and points at the fix. Reads only; the fix is offered, not applied.
func reportProfileIntegrity(w io.Writer) {
	pm, err := newProfileManager()
	if err != nil {
		return
	}
	profiles, err := pm.List()
	if err != nil || len(profiles) == 0 {
		return
	}
	reg, err := newRegistry()
	if err != nil {
		return
	}
	srcs, err := reg.List()
	if err != nil {
		return
	}
	known := make(map[string]bool, len(srcs))
	for _, s := range srcs {
		known[s.Name] = true
	}

	var problems []string
	for _, p := range profiles {
		for _, src := range p.Sources {
			if !known[src] {
				problems = append(problems, fmt.Sprintf("  ✗ profile %q references unregistered source %q", p.Name, src))
			}
		}
	}
	if len(problems) == 0 {
		return
	}
	fmt.Fprintln(w, "\nProfile integrity:")
	for _, p := range problems {
		fmt.Fprintln(w, p)
	}
	fmt.Fprintln(w, "  fix: 'weft source rename <old> <new>' (renames + updates profiles), or edit the profile's sources list.")
}

// lintSources returns the registered sources as pathlint inputs, with roots
// expanded to absolute paths.
func lintSources() ([]pathlint.Source, error) {
	reg, err := newRegistry()
	if err != nil {
		return nil, err
	}
	list, err := reg.List()
	if err != nil {
		return nil, err
	}
	out := make([]pathlint.Source, 0, len(list))
	for _, s := range list {
		out = append(out, pathlint.Source{Name: s.Name, Root: locate.ExpandHome(s.Root)})
	}
	return out, nil
}

// reportPaths scans registered sources and prints a summary of path-reference
// findings. Healable findings show their suggested anchor rewrite.
func reportPaths(w io.Writer) {
	srcs, err := lintSources()
	if err != nil || len(srcs) == 0 {
		return
	}
	findings, err := pathlint.Scan(srcs)
	if err != nil {
		fmt.Fprintf(w, "\nPath lint: error scanning sources: %v\n", err)
		return
	}
	// By default show only actionable findings (healable + unresolved anchors);
	// --all also lists informational external/dead references.
	var shown []pathlint.Finding
	var fixable, hidden int
	byKind := map[pathlint.Kind]int{}
	for _, f := range findings {
		if f.Fixable() {
			fixable++
		}
		if doctorAll || f.Actionable() {
			shown = append(shown, f)
			byKind[f.Kind]++
		} else {
			hidden++
		}
	}

	if len(shown) == 0 {
		fmt.Fprintln(w, "\nPath references: ✓ nothing actionable")
		if hidden > 0 {
			fmt.Fprintf(w, "  (%d informational external/dead reference(s); see 'weft doctor --all')\n", hidden)
		}
		return
	}

	fmt.Fprintf(w, "\nPath references — %d shown, %d healable:\n", len(shown), fixable)
	for _, k := range sortedKinds(byKind) {
		fmt.Fprintf(w, "  %-20s %d\n", string(k)+":", byKind[k])
	}
	for _, f := range shown {
		mark := "•"
		if f.Fixable() {
			mark = "✎"
		}
		fmt.Fprintf(w, "  %s [%s] %s:%d  %s\n", mark, f.Kind, f.File, f.Line, f.Ref)
		if f.Fixable() {
			fmt.Fprintf(w, "        → %s\n", f.Suggestion)
		}
	}
	if hidden > 0 {
		fmt.Fprintf(w, "  … %d informational external/dead reference(s) hidden; see 'weft doctor --all'\n", hidden)
	}
	if fixable > 0 && !doctorFix {
		fmt.Fprintf(w, "\nRun 'weft doctor --fix' to rewrite the %d healable reference(s).\n", fixable)
	}
}

// fixPaths applies the healable path rewrites and reports the result.
func fixPaths(w io.Writer) error {
	srcs, err := lintSources()
	if err != nil {
		return err
	}
	findings, err := pathlint.Scan(srcs)
	if err != nil {
		return err
	}
	changed, err := pathlint.Apply(findings)
	if err != nil {
		return err
	}
	var fixable int
	for _, f := range findings {
		if f.Fixable() {
			fixable++
		}
	}
	fmt.Fprintf(w, "\n✓ Healed %d reference(s) across %d file(s).\n", fixable, changed)
	if changed > 0 {
		fmt.Fprintln(w, "  Re-run 'weft profile use <name>' to re-project, and commit the source changes.")
	}
	return nil
}

// sortedKinds returns the kinds present in m in a stable, readable order.
func sortedKinds(m map[pathlint.Kind]int) []pathlint.Kind {
	kinds := make([]pathlint.Kind, 0, len(m))
	for k := range m {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	return kinds
}

// scanHarnesses lists every known harness with the evidence that identified it.
//
// It reports the signal that actually matched rather than the harness's config
// path: detection succeeds on a binary found on PATH just as often as on a
// config directory, and naming the directory in that case points users at a
// path that need not exist.
func scanHarnesses(w io.Writer) {
	fmt.Fprintln(w, "\nScanning for AI harnesses:")
	for _, k := range harness.All() {
		detected := k.H.Detect()
		displayPath := k.ConfigPath
		if cp, ok := k.H.(harness.ConfigPather); ok {
			displayPath = cp.ConfigPath()
		}

		if !detected {
			looked := harness.DetectSignals(k.H)
			if looked == "" {
				looked = displayPath
			}
			fmt.Fprintf(w, "  – %-13s not found (looked for %s)\n", k.H.Name(), looked)
			continue
		}

		evidence := displayPath
		if r, ok := k.H.(harness.DetectReporter); ok {
			switch via, detail := r.DetectedVia(); via {
			case harness.DetectConfigDir:
				evidence = detail
			case harness.DetectBinary:
				evidence = detail + " (on PATH)"
			case harness.DetectNone:
				// Detect said yes but reported no signal; fall back to the path.
			}
		}
		fmt.Fprintf(w, "  ✓ %-13s %s\n", k.H.Name(), evidence)
	}
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorFix, "fix", false, "rewrite healable path references to weft anchors, and delete untracked project files holding only weft's block")
	doctorCmd.Flags().BoolVar(&doctorAll, "all", false, "also list informational external/dead path references")
	rootCmd.AddCommand(doctorCmd)
}
