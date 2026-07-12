package cmd

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/pathlint"
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
and {{weft.source:NAME}} anchors.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		runDoctor(os.Stdout)
		if doctorFix {
			return fixPaths(os.Stdout)
		}
		return nil
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

	fmt.Fprintln(w, "\nScanning for AI rule folders:")
	for _, k := range harness.All() {
		detected := k.H.Detect()
		displayPath := k.ConfigPath
		if cp, ok := k.H.(harness.ConfigPather); ok {
			displayPath = cp.ConfigPath()
		}
		if detected {
			fmt.Fprintf(w, "  ✓ Found: %s\n", displayPath)
		} else {
			fmt.Fprintf(w, "  – Not found: %s\n", displayPath)
		}
	}

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
	reportPaths(w)
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

func init() {
	doctorCmd.Flags().BoolVar(&doctorFix, "fix", false, "rewrite healable path references to weft anchors")
	doctorCmd.Flags().BoolVar(&doctorAll, "all", false, "also list informational external/dead path references")
	rootCmd.AddCommand(doctorCmd)
}
