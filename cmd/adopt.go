package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/source"
)

var (
	adoptScan  bool
	adoptInto  string
	adoptForce bool
	adoptYes   bool
)

var adoptCmd = &cobra.Command{
	Use:   "adopt [<harness> <path>...]",
	Short: "Bring a harness-native file under weft management",
	Long: `Copy a file you authored inside a harness (e.g. ~/.claude/agents/reviewer.md)
into one of your sources, so weft can project it to every other harness.

    weft adopt --scan                                  # list unowned files
    weft adopt claude-code agents/reviewer.md --into personal-tech

Adoption is explicit and one-way: once a source owns the file, weft overwrites
it on every subsequent apply. Nothing is adopted without confirmation (--yes to
skip the prompt), a file is never copied over an existing one without --force,
and a file carrying what looks like a literal credential is always refused —
sources are ordinary git repos that get pushed.

Only commands, agents and skills are adoptable, and only markdown files.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgDir := configDir()
		if cfgDir == "" {
			return fmt.Errorf("resolving config directory")
		}
		targets, err := adoptTargets(cfgDir)
		if err != nil {
			return err
		}
		if adoptScan {
			if len(args) > 0 {
				return fmt.Errorf("--scan takes no arguments — drop them, or drop --scan to adopt")
			}
			return runAdoptScan(cmd.OutOrStdout(), targets)
		}
		if len(args) < 2 {
			return fmt.Errorf("usage: weft adopt <harness> <path>... --into <source> (or 'weft adopt --scan')")
		}
		return runAdopt(cmd.OutOrStdout(), cfgDir, targets, args[0], args[1:])
	},
}

// adoptTargets builds the scan set: every harness weft has a manifest for (its
// target root is recorded there), plus the active profile's targets that have
// never been applied — a fresh install has no manifests but may well have a
// ~/.claude full of hand-written agents.
func adoptTargets(cfgDir string) ([]harness.ScanTarget, error) {
	reg := harness.NewRegistry(harness.Instances()...)
	roots := map[string]string{} // harness name -> target root

	for _, name := range manifestHarnessNames(cfgDir) {
		m, err := manifest.Load(cfgDir, name)
		if err != nil || m.TargetRoot == "" {
			continue
		}
		roots[name] = m.TargetRoot
	}
	for _, name := range activeProfileTargets() {
		if _, ok := roots[name]; ok {
			continue
		}
		if root := knownConfigRoot(name); root != "" {
			roots[name] = root
		}
	}

	names := make([]string, 0, len(roots))
	for name := range roots {
		names = append(names, name)
	}
	sort.Strings(names)

	targets := make([]harness.ScanTarget, 0, len(names))
	for _, name := range names {
		root := roots[name]
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue // harness declared but not present on this machine
		}
		m, err := manifest.Load(cfgDir, name)
		if err != nil {
			return nil, fmt.Errorf("loading manifest for %s: %w", name, err)
		}
		h, _ := reg.Get(name) // nil for a harness with no adapter — Scan handles that
		targets = append(targets, harness.ScanTarget{
			Harness: name, Root: root, Owned: m.Files, H: h, CfgDir: cfgDir,
		})
	}
	return targets, nil
}

// manifestHarnessNames lists the harnesses weft has applied to, read from the
// manifest filenames.
func manifestHarnessNames(cfgDir string) []string {
	entries, err := os.ReadDir(filepath.Join(cfgDir, "manifests"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return names
}

// activeProfileTargets returns the active profile's harness names, or nil when
// there is no active profile. A missing profile is not an error here — scanning
// the applied harnesses alone is still useful.
func activeProfileTargets() []string {
	name := activeProfileName()
	if name == "" {
		return nil
	}
	pm, err := newProfileManager()
	if err != nil {
		return nil
	}
	p, err := pm.Get(name)
	if err != nil {
		return nil
	}
	return p.ResolvedTargets()
}

// knownConfigRoot resolves a harness's config directory from the registry's
// static path or its runtime ConfigPather, expanding "~".
func knownConfigRoot(name string) string {
	for _, k := range harness.All() {
		if k.H.Name() != name {
			continue
		}
		path := k.ConfigPath
		if cp, ok := k.H.(harness.ConfigPather); ok {
			path = cp.ConfigPath()
		}
		return locate.ExpandHome(path)
	}
	return ""
}

func runAdoptScan(out io.Writer, targets []harness.ScanTarget) error {
	candidates, err := harness.Scan(targets)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Fprintln(out, "No adoptable files — every command, agent and skill in your harnesses is already source-owned.")
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Harness != candidates[j].Harness {
			return candidates[i].Harness < candidates[j].Harness
		}
		return candidates[i].Rel < candidates[j].Rel
	})

	suggested := suggestedSource()
	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "HARNESS\tCLASS\tPATH")
	for _, c := range candidates {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Harness, c.Class, c.Rel)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("writing scan table: %w", err)
	}
	fmt.Fprintf(out, "\n%d adoptable file(s). Adopt with:\n", len(candidates))
	fmt.Fprintf(out, "  weft adopt %s %s --into %s\n", candidates[0].Harness, candidates[0].Rel, suggested)
	return nil
}

// suggestedSource names a source for the scan's example command: the profile's
// only source when there is exactly one, otherwise a placeholder — guessing
// between several would put a wrong-but-runnable command in front of the user.
func suggestedSource() string {
	names, err := adoptSourceCandidates()
	if err != nil || len(names) != 1 {
		return "<source>"
	}
	return names[0]
}

func runAdopt(out io.Writer, cfgDir string, targets []harness.ScanTarget, harnessName string, paths []string) error {
	var target harness.ScanTarget
	found := false
	for _, t := range targets {
		if t.Harness == harnessName {
			target, found = t, true
			break
		}
	}
	if !found {
		return fmt.Errorf(
			"no target root for harness %q — run 'weft adopt --scan' to see the harnesses weft can adopt from",
			harnessName)
	}
	target.CfgDir = cfgDir

	layout, err := resolveAdoptLayout(adoptInto)
	if err != nil {
		return err
	}

	req := harness.AdoptRequest{
		Target: target, Rels: paths, Layout: layout,
		Force: adoptForce, Confirmed: adoptYes,
	}
	entries, err := harness.Adopt(req)
	if errors.Is(err, harness.ErrConfirmRequired) {
		printAdoptPlan(out, harnessName, layout, entries)
		if !confirm("Adopt these file(s)? (y/N) ") {
			fmt.Fprintln(out, "Aborted.")
			return nil
		}
		req.Confirmed = true
		entries, err = harness.Adopt(req)
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		fmt.Fprintf(out, "  ✓ %s → %s\n", e.Rel, filepath.Join(layout.Name, e.DestRel))
	}
	fmt.Fprintf(out, "✓ adopted %d file(s) into source %q\n", len(entries), layout.Name)
	fmt.Fprintln(out, "  weft now owns these files — run 'weft profile use <name>' to fan them out.")
	return nil
}

// printAdoptPlan shows exactly what will happen before the prompt. Adoption is
// irreversible in the sense that matters (weft starts overwriting the file), so
// the preview names both ends of every copy rather than just a count.
func printAdoptPlan(out io.Writer, harnessName string, layout harness.SourceLayout, entries []harness.AdoptEntry) {
	fmt.Fprintf(out, "Adopt from %s into source %q (%s):\n", harnessName, layout.Name, locate.Tilde(layout.Root))
	for _, e := range entries {
		note := ""
		if e.Overwrite {
			note = "  (overwrites existing)"
		}
		fmt.Fprintf(out, "  %-9s %s → %s%s\n", e.Class, e.Rel, e.DestRel, note)
	}
	fmt.Fprintln(out, "Once adopted, weft overwrites these files in every harness on each apply.")
}

// resolveAdoptLayout picks the destination source and reads its directory
// layout. An omitted --into resolves only when there is exactly one candidate;
// with several, guessing would silently put the file in the wrong repo.
func resolveAdoptLayout(name string) (harness.SourceLayout, error) {
	reg, err := newRegistry()
	if err != nil {
		return harness.SourceLayout{}, err
	}
	if name == "" {
		candidates, cErr := adoptSourceCandidates()
		if cErr != nil {
			return harness.SourceLayout{}, cErr
		}
		switch len(candidates) {
		case 0:
			return harness.SourceLayout{}, fmt.Errorf("no sources registered — run 'weft source add <name> <path>' first")
		case 1:
			name = candidates[0]
		default:
			return harness.SourceLayout{}, fmt.Errorf(
				"--into is required: several sources could receive this file (%s)",
				strings.Join(candidates, ", "))
		}
	}
	s, err := reg.Get(name)
	if err != nil {
		return harness.SourceLayout{}, err
	}
	return layoutFromSource(*s), nil
}

// layoutFromSource maps a source's configured structure onto the class
// directories adoption writes to, defaulting to the standard layout for
// sources that do not set one.
func layoutFromSource(s source.Source) harness.SourceLayout {
	st := s.Structure
	def := source.DefaultStructure()
	return harness.SourceLayout{
		Name:     s.Name,
		Root:     locate.ExpandHome(s.Root),
		Commands: firstNonEmpty(st.Commands, def.Commands),
		Agents:   firstNonEmpty(st.Agents, def.Agents),
		Skills:   firstNonEmpty(st.Skills, def.Skills),
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// adoptSourceCandidates lists the sources eligible to receive an adopted file:
// the active profile's sources when there is one, otherwise every registered
// source. The profile's list is the narrower, more accurate answer — a source
// the active profile does not read cannot fan the file out.
func adoptSourceCandidates() ([]string, error) {
	if name := activeProfileName(); name != "" {
		if pm, err := newProfileManager(); err == nil {
			if p, pErr := pm.Get(name); pErr == nil && len(p.Sources) > 0 {
				return p.Sources, nil
			}
		}
	}
	reg, err := newRegistry()
	if err != nil {
		return nil, err
	}
	sources, err := reg.List()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = s.Name
	}
	return names, nil
}

func init() {
	adoptCmd.Flags().BoolVar(&adoptScan, "scan", false, "list harness files that no source owns yet")
	adoptCmd.Flags().StringVar(&adoptInto, "into", "", "source to adopt into (required when the profile has more than one)")
	adoptCmd.Flags().BoolVar(&adoptForce, "force", false, "overwrite a file that already exists in the source")
	adoptCmd.Flags().BoolVar(&adoptYes, "yes", false, "skip the confirmation prompt")
	rootCmd.AddCommand(adoptCmd)
}
