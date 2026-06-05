package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/collect"
	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/diff"
	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/merge"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/watch"
)

// newProfileManager builds a FileManager using the configured profiles directory.
func newProfileManager() *profile.FileManager {
	dir := viper.GetString("profiles_dir")
	if dir == "" {
		cfg, _ := config.Defaults()
		dir = cfg.ProfilesDir
	}
	return profile.NewFileManager(dir)
}

// managedFilter returns a merge.Filter that restricts merging to the union of
// managed paths across all sources: CLAUDE.md + each Structure subdirectory.
func managedFilter(sources []source.Source) merge.Filter {
	// Build the set of managed root-relative prefixes.
	prefixes := []string{"CLAUDE.md"}
	seen := map[string]bool{"CLAUDE.md": true}
	for _, s := range sources {
		for _, d := range []string{
			s.Structure.Commands,
			s.Structure.Agents,
			s.Structure.Skills,
			s.Structure.Memory,
			s.Structure.Hooks,
		} {
			d = strings.TrimSuffix(strings.TrimSpace(d), "/")
			if d != "" && !seen[d] {
				prefixes = append(prefixes, d)
				seen[d] = true
			}
		}
	}
	return func(rel string) bool {
		for _, p := range prefixes {
			if rel == p || strings.HasPrefix(rel, p+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
}

// buildAssembler returns a merge.Assembler that collects instruction files for
// each root using the InstructionGlob configured in the corresponding source.
// Managed subdirectory files (commands, agents, etc.) are always excluded so
// they are never assembled into the instruction content.
func buildAssembler(roots []string, srcs []source.Source) merge.Assembler {
	type entry struct {
		glob     string
		excludes []string
	}
	byRoot := make(map[string]entry, len(roots))
	for i, root := range roots {
		s := srcs[i]
		glob := s.Structure.InstructionGlob
		if glob == "" {
			glob = source.DefaultStructure().InstructionGlob
		}
		var excludes []string
		for _, d := range []string{
			s.Structure.Commands,
			s.Structure.Agents,
			s.Structure.Skills,
			s.Structure.Memory,
			s.Structure.Hooks,
		} {
			if d = strings.TrimRight(strings.TrimSpace(d), "/\\"); d != "" {
				excludes = append(excludes, d)
			}
		}
		byRoot[root] = entry{glob: glob, excludes: excludes}
	}
	return func(root string) ([]byte, error) {
		e := byRoot[root]
		return collect.Collect(root, e.glob, e.excludes...)
	}
}

// resolveProfileRoots loads a profile by name, expands every source root, and
// verifies each root exists on disk. Returns the profile, expanded root paths,
// and the corresponding Source values in the same order.
func resolveProfileRoots(name string) (*profile.Profile, []string, []source.Source, error) {
	p, err := newProfileManager().Get(name)
	if err != nil {
		return nil, nil, nil, err
	}
	reg := newRegistry()
	var roots []string
	var srcs []source.Source
	for _, srcName := range p.Sources {
		s, err := reg.Get(srcName)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("source %q referenced by profile not found: %w", srcName, err)
		}
		expanded := source.ExpandHome(s.Root)
		if _, err := os.Stat(expanded); err != nil {
			return nil, nil, nil, fmt.Errorf(
				"source %q root %s does not exist — clone or create it first",
				s.Name, s.Root,
			)
		}
		roots = append(roots, expanded)
		srcs = append(srcs, *s)
	}
	return p, roots, srcs, nil
}

// stageProfile merges the profile's sources into outputDir and returns the
// manifest. It does not apply the result to any harness.
func stageProfile(p *profile.Profile, roots []string, srcs []source.Source, outputDir string) ([]string, error) {
	if err := os.RemoveAll(outputDir); err != nil {
		return nil, fmt.Errorf("clearing output dir: %w", err)
	}
	return merge.New(p.Overlay).
		WithFilter(managedFilter(srcs)).
		WithAssembler(buildAssembler(roots, srcs)).
		MergeRoots(roots, outputDir)
}

// parseSources splits a comma-separated source list and trims whitespace.
func parseSources(raw string) []string {
	var names []string
	for _, s := range strings.Split(raw, ",") {
		if name := strings.TrimSpace(s); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// activeProfileName returns the currently active profile name from config.
func activeProfileName() string {
	return viper.GetString("active_profile")
}

// ── Flags ─────────────────────────────────────────────────────────────────────

var (
	profileSources string
	profileOverlay string
	profileTarget  string
	profileWatch   bool
	inspectFormat  string
)

// mergeAndApply runs the merge+apply pipeline for a resolved profile.
// quiet suppresses informational output (used during watch re-applies).
func mergeAndApply(p *profile.Profile, roots []string, srcs []source.Source, cfgDir string, quiet bool) error {
	stagedDir := filepath.Join(cfgDir, "staged", p.Name)

	if !quiet {
		fmt.Printf("Merging %d source(s) [%s] with strategy %q...\n",
			len(roots), strings.Join(p.Sources, ", "), p.Overlay)
	}
	manifest, err := stageProfile(p, roots, srcs, stagedDir)
	if err != nil {
		return fmt.Errorf("merging sources: %w", err)
	}
	if !quiet {
		fmt.Printf("  %d file(s) merged into staging\n", len(manifest))
	}

	target := p.ActiveTarget
	if target == "" {
		if (&harness.ClaudeCode{}).Detect() {
			target = "claude-code"
			if !quiet {
				fmt.Printf("  no target set — auto-detected: claude-code\n")
			}
		}
	}
	if target == "" {
		if !quiet {
			fmt.Println("  no harness target — staged output is at:", stagedDir)
		}
		return nil
	}

	hReg := harness.NewRegistry(harness.Instances()...)
	h, ok := hReg.Get(target)
	if !ok {
		return fmt.Errorf("unknown harness %q — run 'weft target list' to see supported harnesses", target)
	}
	if !quiet {
		fmt.Printf("Applying to %s...\n", target)
	}
	if err := h.Apply(stagedDir, harness.ApplyCtx{ProfileName: p.Name, CfgDir: cfgDir}); err != nil {
		return fmt.Errorf("applying to %s: %w", target, err)
	}
	return nil
}

// ── Commands ──────────────────────────────────────────────────────────────────

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage rule profiles (named combinations of sources)",
}

var profileCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a profile combining multiple sources",
	Long: `Create a named profile that layers one or more sources.

  <name>       profile identifier, e.g. "hybrid" or "work-only"
  --sources    comma-separated source names that must already be registered
  --overlay    how to resolve conflicts: cascade (default) | merge | last-wins
  --target     default harness to apply to: claude-code | cursor | warp`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		names := parseSources(profileSources)
		if len(names) == 0 {
			return fmt.Errorf("--sources is required and cannot be empty")
		}

		// Verify every referenced source is registered.
		reg := newRegistry()
		for _, name := range names {
			if _, err := reg.Get(name); err != nil {
				return fmt.Errorf(
					"source %q not found — register it first with:\n  weft source add %s <path> <remote>",
					name, name,
				)
			}
		}

		p := profile.Profile{
			Name:         args[0],
			Sources:      names,
			Overlay:      profile.Overlay(profileOverlay),
			ActiveTarget: profileTarget,
		}
		if err := newProfileManager().Create(p); err != nil {
			return err
		}

		fmt.Printf("✓ Profile %q created\n", p.Name)
		fmt.Printf("  sources:  %s\n", strings.Join(names, ", "))
		fmt.Printf("  overlay:  %s\n", p.Overlay)
		if p.ActiveTarget != "" {
			fmt.Printf("  target:   %s\n", p.ActiveTarget)
		}
		fmt.Printf("\nActivate with: weft profile use %s\n", p.Name)
		return nil
	},
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		profiles, err := newProfileManager().List()
		if err != nil {
			return err
		}
		if len(profiles) == 0 {
			fmt.Println("No profiles created.")
			fmt.Println("Create one with: weft profile create <name> --sources <source,...>")
			return nil
		}

		active := activeProfileName()

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tSOURCES\tOVERLAY\tTARGET\tSTATUS")
		for _, p := range profiles {
			status := ""
			if active != "" && p.Name == active {
				status = "active"
			}
			target := p.ActiveTarget
			if target == "" {
				target = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				p.Name,
				strings.Join(p.Sources, ", "),
				p.Overlay,
				target,
				status,
			)
		}
		return w.Flush()
	},
}

var profileUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Activate a profile: merge sources and apply to the target harness",
	Long: `Activate a profile by merging its sources and writing the result to the harness config.

With --watch the command stays running and re-applies automatically whenever a
file inside any source root changes. Press Ctrl-C to stop watching.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		// 1. Load the profile and resolve source roots.
		p, roots, srcs, err := resolveProfileRoots(name)
		if err != nil {
			return err
		}

		// 2. Resolve config dir (used for staging).
		cfgDir, err := config.DefaultDir()
		if err != nil {
			return err
		}
		stagedDir := filepath.Join(cfgDir, "staged", name)

		// 4. Initial merge + apply.
		if err := mergeAndApply(p, roots, srcs, cfgDir, false); err != nil {
			return err
		}

		// 5. Persist the active profile in config.yaml.
		if err := config.SetActiveProfile(name); err != nil {
			return fmt.Errorf("saving active profile: %w", err)
		}

		fmt.Printf("\n✓ Profile %q is now active\n", name)
		if p.ActiveTarget != "" {
			fmt.Printf("  target: %s\n", p.ActiveTarget)
		}
		fmt.Printf("  staged: %s\n", stagedDir)

		// 6. Enter watch mode if requested.
		if profileWatch {
			fmt.Println("\nWatching for changes... (Ctrl-C to stop)")
			stop, err := watch.Debounced(roots, 300*time.Millisecond, func() {
				fmt.Printf("\n[weft] change detected — re-applying...\n")
				if applyErr := mergeAndApply(p, roots, srcs, cfgDir, true); applyErr != nil {
					fmt.Fprintf(os.Stderr, "[weft] error: %v\n", applyErr)
					return
				}
				fmt.Printf("[weft] ✓ applied at %s\n", time.Now().Format("15:04:05"))
			})
			if err != nil {
				return fmt.Errorf("starting watcher: %w", err)
			}

			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			<-sig
			stop()
			fmt.Println("\nWatcher stopped.")
		}

		return nil
	},
}

var diffVerbose bool

var profileDiffCmd = &cobra.Command{
	Use:   "diff <profile-a> <profile-b>",
	Short: "Show what changes when switching from profile-a to profile-b",
	Long: `Stage both profiles and compare the merged outputs file by file.

Summary (default): lists added, removed, and changed files with counts.
Verbose (--verbose / -v): also shows line-level diffs for every changed,
added, or removed file.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		nameA, nameB := args[0], args[1]

		// 1. Resolve both profiles.
		pA, rootsA, srcsA, err := resolveProfileRoots(nameA)
		if err != nil {
			return fmt.Errorf("profile %q: %w", nameA, err)
		}
		pB, rootsB, srcsB, err := resolveProfileRoots(nameB)
		if err != nil {
			return fmt.Errorf("profile %q: %w", nameB, err)
		}

		// 2. Stage both into temp directories.
		dirA, err := os.MkdirTemp("", "weft-diff-a-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(dirA) }()

		dirB, err := os.MkdirTemp("", "weft-diff-b-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(dirB) }()

		if _, err := stageProfile(pA, rootsA, srcsA, dirA); err != nil {
			return fmt.Errorf("staging %q: %w", nameA, err)
		}
		if _, err := stageProfile(pB, rootsB, srcsB, dirB); err != nil {
			return fmt.Errorf("staging %q: %w", nameB, err)
		}

		// 3. Compare.
		files, err := diff.Compare(dirA, dirB)
		if err != nil {
			return fmt.Errorf("comparing profiles: %w", err)
		}

		// 4. Print.
		printProfileDiff(nameA, nameB, pA, pB, files, dirA, dirB, diffVerbose)
		return nil
	},
}

func printProfileDiff(nameA, nameB string, pA, pB *profile.Profile, files []diff.File, dirA, dirB string, verbose bool) {
	fmt.Printf("Diff  %s → %s\n", nameA, nameB)
	if pA.Overlay != pB.Overlay {
		fmt.Printf("  strategy: %s → %s\n", pA.Overlay, pB.Overlay)
	}
	fmt.Println()

	var added, removed, changed, same int
	for _, f := range files {
		switch f.Kind {
		case diff.Added:
			added++
		case diff.Removed:
			removed++
		case diff.Changed:
			changed++
		case diff.Same:
			same++
		}
	}

	if added+removed+changed == 0 {
		fmt.Println("  No differences — profiles produce identical output.")
		fmt.Printf("  %d file(s) unchanged\n", same)
		return
	}

	if !verbose {
		// Summary: one line per non-same file.
		for _, f := range files {
			switch f.Kind {
			case diff.Added:
				fmt.Printf("  + %s\n", f.Rel)
			case diff.Removed:
				fmt.Printf("  - %s\n", f.Rel)
			case diff.Changed:
				fmt.Printf("  ~ %s\n", f.Rel)
			}
		}
		fmt.Println()
		fmt.Printf("  %d added  %d removed  %d changed  %d unchanged\n",
			added, removed, changed, same)
		return
	}

	// Verbose: line-level diff per changed/added/removed file.
	const separator = "────────────────────────────────────────────────────────"
	for _, f := range files {
		switch f.Kind {
		case diff.Same:
			continue
		case diff.Changed:
			fmt.Printf("~ %s\n%s\n", f.Rel, separator)
			contA, _ := os.ReadFile(filepath.Join(dirA, f.Rel))
			contB, _ := os.ReadFile(filepath.Join(dirB, f.Rel))
			fmt.Print(diff.LineDiff(string(contA), string(contB)))
		case diff.Added:
			fmt.Printf("+ %s\n%s\n", f.Rel, separator)
			fmt.Print(diff.ContentLines(filepath.Join(dirB, f.Rel), "+ ", diff.ColorCodeGreen))
		case diff.Removed:
			fmt.Printf("- %s\n%s\n", f.Rel, separator)
			fmt.Print(diff.ContentLines(filepath.Join(dirA, f.Rel), "- ", diff.ColorCodeRed))
		}
		fmt.Println()
	}
	fmt.Printf("%d added  %d removed  %d changed  %d unchanged\n",
		added, removed, changed, same)
}

var profileInspectCmd = &cobra.Command{
	Use:   "inspect <name>",
	Short: "Dry-run a profile: show conflicts and merge winners without applying",
	Long: `Inspect resolves a profile's sources and reports which files conflict,
which source wins, and what the merge result will be — without writing anything to disk.

Formats:
  --format text     human-readable table (default)
  --format mermaid  flowchart showing the merge topology`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		p, roots, srcs, err := resolveProfileRoots(name)
		if err != nil {
			return err
		}

		sourceNames := p.Sources
		report, err := merge.New(p.Overlay).
			WithFilter(managedFilter(srcs)).
			Inspect(roots)
		if err != nil {
			return fmt.Errorf("inspecting sources: %w", err)
		}

		rootToName := make(map[string]string, len(roots))
		for i, root := range roots {
			rootToName[root] = sourceNames[i]
		}

		switch inspectFormat {
		case "mermaid":
			printInspectMermaid(report, rootToName, sourceNames)
		default:
			printInspectText(report, rootToName, sourceNames, p)
		}
		return nil
	},
}

// printInspectText renders a human-readable conflict report.
func printInspectText(report *merge.InspectReport, rootToName map[string]string, sourceNames []string, p *profile.Profile) {
	conflicts := report.Conflicts()
	unique := report.Unique()

	fmt.Printf("Profile %q — inspect\n", p.Name)
	fmt.Printf("  strategy: %s\n", p.Overlay)
	fmt.Printf("  sources:  %s\n", strings.Join(sourceNames, " → "))
	fmt.Println()

	if len(conflicts) > 0 {
		fmt.Printf("Conflicts — %d file(s):\n", len(conflicts))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "  FILE\tIN SOURCES\tWINNER\tNOTE")
		for _, e := range conflicts {
			srcNames := make([]string, len(e.Roots))
			for i, root := range e.Roots {
				srcNames[i] = rootToName[root]
			}
			winner, note := "all (merged)", "all sources combined"
			if e.WinnerRoot != "" {
				winner = rootToName[e.WinnerRoot]
				note = "last overlay wins"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n",
				e.Rel, strings.Join(srcNames, ", "), winner, note)
		}
		_ = w.Flush()
		fmt.Println()
	} else {
		fmt.Println("No conflicts — all files are unique across sources.")
		fmt.Println()
	}

	if len(unique) > 0 {
		fmt.Printf("Unique — %d file(s):\n", len(unique))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "  FILE\tSOURCE")
		for _, e := range unique {
			fmt.Fprintf(w, "  %s\t%s\n", e.Rel, rootToName[e.Roots[0]])
		}
		_ = w.Flush()
		fmt.Println()
	}

	fmt.Printf("Total: %d file(s)  (%d conflict, %d unique)\n",
		len(report.Entries), len(conflicts), len(unique))
}

// printInspectMermaid renders a flowchart showing the merge topology.
func printInspectMermaid(report *merge.InspectReport, rootToName map[string]string, sourceNames []string) {
	fmt.Println("```mermaid")
	fmt.Println("flowchart LR")

	for _, name := range sourceNames {
		fmt.Printf("  s_%s[\"%s\"]\n", mermaidNodeID(name), name)
	}

	conflicts := report.Conflicts()
	for _, e := range conflicts {
		nodeID := "f_" + mermaidNodeID(e.Rel)
		fmt.Printf("  %s[\"%s\"]\n", nodeID, e.Rel)
		for _, root := range e.Roots {
			srcID := "s_" + mermaidNodeID(rootToName[root])
			switch {
			case e.WinnerRoot == "":
				fmt.Printf("  %s -->|merged| %s\n", srcID, nodeID)
			case root == e.WinnerRoot:
				fmt.Printf("  %s -->|wins| %s\n", srcID, nodeID)
			default:
				fmt.Printf("  %s --> %s\n", srcID, nodeID)
			}
		}
	}

	// Per-source unique file count summary nodes.
	uniqueCount := make(map[string]int, len(sourceNames))
	for _, e := range report.Unique() {
		uniqueCount[rootToName[e.Roots[0]]]++
	}
	for _, name := range sourceNames {
		n := uniqueCount[name]
		if n == 0 {
			continue
		}
		uID := "u_" + mermaidNodeID(name)
		fmt.Printf("  %s[\"%d unique file(s)\"]\n", uID, n)
		fmt.Printf("  s_%s --> %s\n", mermaidNodeID(name), uID)
	}

	// Class assignments.
	if len(conflicts) > 0 {
		ids := make([]string, len(conflicts))
		for i, e := range conflicts {
			ids[i] = "f_" + mermaidNodeID(e.Rel)
		}
		fmt.Printf("  class %s conflict\n", strings.Join(ids, ","))
	}
	var uIDs []string
	for _, name := range sourceNames {
		if uniqueCount[name] > 0 {
			uIDs = append(uIDs, "u_"+mermaidNodeID(name))
		}
	}
	if len(uIDs) > 0 {
		fmt.Printf("  class %s unique\n", strings.Join(uIDs, ","))
	}

	fmt.Println()
	fmt.Println("  classDef conflict fill:#fbbf24,stroke:#f59e0b,color:#1f2937,font-weight:bold")
	fmt.Println("  classDef unique fill:#e5e7eb,stroke:#d1d5db,color:#374151")
	fmt.Println("```")
}

// mermaidNodeID sanitizes a string for use as a Mermaid node identifier.
func mermaidNodeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

var profileDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a profile (does not affect sources)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := newProfileManager().Delete(args[0]); err != nil {
			return err
		}
		fmt.Printf("✓ Profile %q deleted\n", args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(profileCmd)
	profileCmd.AddCommand(
		profileCreateCmd,
		profileUseCmd,
		profileListCmd,
		profileInspectCmd,
		profileDiffCmd,
		profileDeleteCmd,
	)

	profileCreateCmd.Flags().StringVar(&profileSources, "sources", "", "comma-separated source names (required)")
	profileCreateCmd.Flags().StringVar(&profileOverlay, "overlay", "cascade", "cascade|merge|last-wins")
	profileCreateCmd.Flags().StringVar(&profileTarget, "target", "", "default harness: claude-code|cursor|warp")
	_ = profileCreateCmd.MarkFlagRequired("sources")

	profileUseCmd.Flags().BoolVar(&profileWatch, "watch", false, "re-apply automatically when source files change")

	profileInspectCmd.Flags().StringVar(&inspectFormat, "format", "text", "output format: text|mermaid")

	profileDiffCmd.Flags().BoolVarP(&diffVerbose, "verbose", "v", false, "show line-level diff for changed, added, and removed files")
}
