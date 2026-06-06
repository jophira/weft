package cmd

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
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
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/merge"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/validate"
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
		s.Root = expanded
		roots = append(roots, expanded)
		srcs = append(srcs, *s)
	}
	return p, roots, srcs, nil
}

// stageProfile merges the profile's sources into outputDir. Returns the sorted
// list of written paths and an attribution map (rel -> contributing root
// indices) for files assembled from more than one root.
func stageProfile(p *profile.Profile, roots []string, srcs []source.Source, outputDir string) ([]string, map[string][]int, error) {
	if err := os.RemoveAll(outputDir); err != nil {
		return nil, nil, fmt.Errorf("clearing output dir: %w", err)
	}
	return merge.New(p.Overlay).
		WithFilter(managedFilter(srcs)).
		WithAssembler(buildAssembler(roots, srcs)).
		MergeRoots(roots, outputDir)
}

// sourceAttribution converts root-index attribution from stageProfile into
// source-name attribution suitable for storing in the manifest.
func sourceAttribution(attribution map[string][]int, srcs []source.Source) map[string][]string {
	if len(attribution) == 0 {
		return nil
	}
	result := make(map[string][]string, len(attribution))
	for rel, indices := range attribution {
		names := make([]string, len(indices))
		for i, idx := range indices {
			names[i] = srcs[idx].Name
		}
		result[rel] = names
	}
	return result
}

// parseSources splits a comma-separated source list and trims whitespace.
func parseSources(raw string) []string {
	var names []string
	for s := range strings.SplitSeq(raw, ",") {
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
	profileTargets []string
	profileNoWatch bool
	inspectFormat  string
)

// sourceContrib describes one source's byte contribution to the merged CLAUDE.md.
type sourceContrib struct {
	name  string
	bytes int
}

// computeProvenance calls collect.Collect for each source root and returns the
// per-source byte counts for the instruction file.
func computeProvenance(roots []string, srcs []source.Source) []sourceContrib {
	contribs := make([]sourceContrib, len(roots))
	for i, root := range roots {
		s := srcs[i]
		glob := s.Structure.InstructionGlob
		if glob == "" {
			glob = source.DefaultStructure().InstructionGlob
		}
		var excludes []string
		for _, d := range []string{
			s.Structure.Commands, s.Structure.Agents,
			s.Structure.Skills, s.Structure.Memory, s.Structure.Hooks,
		} {
			if d = strings.TrimRight(strings.TrimSpace(d), "/\\"); d != "" {
				excludes = append(excludes, d)
			}
		}
		data, _ := collect.Collect(root, glob, excludes...)
		contribs[i] = sourceContrib{name: s.Name, bytes: len(data)}
	}
	return contribs
}

// fmtBytes formats a byte count as "X B" or "X.Y KB".
func fmtBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

// printQualityReport reads the staged CLAUDE.md, prints a provenance line, and
// emits any size or duplicate-block warnings.
func printQualityReport(stagedDir string, p *profile.Profile, roots []string, srcs []source.Source) {
	content, err := os.ReadFile(filepath.Join(stagedDir, "CLAUDE.md"))
	if err != nil {
		return // no instruction file; nothing to report
	}

	contribs := computeProvenance(roots, srcs)

	if p.Overlay == profile.OverlayMerge && len(contribs) > 1 {
		parts := make([]string, len(contribs))
		for i, c := range contribs {
			parts[i] = fmt.Sprintf("%s: %s", c.name, fmtBytes(c.bytes))
		}
		fmt.Printf("  CLAUDE.md: %s  (%s)\n", fmtBytes(len(content)), strings.Join(parts, ", "))
	} else {
		var winner string
		for i := len(contribs) - 1; i >= 0; i-- {
			if contribs[i].bytes > 0 {
				winner = contribs[i].name
				break
			}
		}
		if winner != "" {
			fmt.Printf("  CLAUDE.md: %s  (from: %s)\n", fmtBytes(len(content)), winner)
		} else {
			fmt.Printf("  CLAUDE.md: %s\n", fmtBytes(len(content)))
		}
	}

	warnKB := viper.GetInt("warn_instruction_size_kb")
	if warnKB <= 0 {
		warnKB = validate.DefaultWarnSizeKB
	}
	r := validate.Instruction(content, warnKB)
	if r.SizeWarning {
		fmt.Printf("  ! CLAUDE.md is %s — long instruction files may reduce model compliance\n", fmtBytes(len(content)))
		fmt.Printf("    (change threshold: weft config set warn-size <KB>)\n")
	}
	for _, dupe := range r.DuplicateBlocks {
		fmt.Printf("  ! duplicate block: %q\n", dupe)
	}
}

// mergeAndApply runs the merge+apply pipeline for a resolved profile.
// quiet suppresses informational output (used during watch re-applies).
func mergeAndApply(p *profile.Profile, roots []string, srcs []source.Source, cfgDir string, quiet bool) error {
	stagedDir := filepath.Join(cfgDir, "staged", p.Name)

	if !quiet {
		fmt.Printf("Merging %d source(s) [%s] with strategy %q...\n",
			len(roots), strings.Join(p.Sources, ", "), p.Overlay)
	}
	staged, rootAttribution, err := stageProfile(p, roots, srcs, stagedDir)
	if err != nil {
		return fmt.Errorf("merging sources: %w", err)
	}
	if !quiet {
		fmt.Printf("  %d file(s) merged into staging\n", len(staged))
		printQualityReport(stagedDir, p, roots, srcs)
	}

	targets := resolveApplyTargets(p, quiet)
	if len(targets) == 0 {
		if !quiet {
			fmt.Println("  no harness target — staged output is at:", stagedDir)
		}
		return nil
	}

	hReg := harness.NewRegistry(harness.Instances()...)
	attr := sourceAttribution(rootAttribution, srcs)

	for _, target := range targets {
		h, ok := hReg.Get(target)
		if !ok {
			return fmt.Errorf("unknown harness %q — run 'weft target list' to see supported harnesses", target)
		}

		// On the initial (non-quiet) apply, write back any externally-modified
		// target files to their source before overwriting them.
		if !quiet {
			if wbErr := startupWriteBack(stagedDir, target, cfgDir, p, srcs); wbErr != nil {
				fmt.Fprintf(os.Stderr, "[weft] startup write-back warning: %v\n", wbErr)
			}
		}

		if !quiet {
			fmt.Printf("Applying to %s...\n", target)
		}
		var applyOut io.Writer
		if !quiet {
			applyOut = os.Stdout
		}
		ctx := harness.ApplyCtx{
			ProfileName:       p.Name,
			CfgDir:            cfgDir,
			SourceAttribution: attr,
			Out:               applyOut,
		}
		if err := h.Apply(stagedDir, ctx); err != nil {
			return fmt.Errorf("applying to %s: %w", target, err)
		}
	}
	return nil
}

// resolveApplyTargets returns the list of harness targets to apply to.
// If the profile has no configured targets, it auto-detects installed harnesses.
func resolveApplyTargets(p *profile.Profile, quiet bool) []string {
	configured := p.ResolvedTargets()
	if len(configured) > 0 {
		return configured
	}
	// Auto-detect: use any installed harness.
	var detected []string
	for _, h := range harness.Instances() {
		if h.Detect() {
			detected = append(detected, h.Name())
		}
	}
	if len(detected) == 1 && !quiet {
		fmt.Printf("  no target set — auto-detected: %s\n", detected[0])
	}
	return detected
}

// harnessTargetRoot returns the target directory last written by the given
// harness, as recorded in its manifest. Returns "" when no manifest exists yet.
func harnessTargetRoot(cfgDir, harnessName string) string {
	m, err := manifest.Load(cfgDir, harnessName)
	if err != nil || m.TargetRoot == "" {
		return ""
	}
	return m.TargetRoot
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
  --target     harness to apply to; repeat for multiple: --target claude-code --target codex`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		names := parseSources(profileSources)
		if len(names) == 0 {
			return fmt.Errorf("--sources is required and cannot be empty")
		}

		// Validate overlay.
		if err := validateOverlay(profileOverlay); err != nil {
			return err
		}

		// Validate targets (optional field — only checked when provided).
		for _, t := range profileTargets {
			if err := validateTarget(t); err != nil {
				return err
			}
		}

		// Verify every referenced source is registered.
		reg := newRegistry()
		for _, name := range names {
			if _, err := reg.Get(name); err != nil {
				registered, _ := reg.List()
				names := make([]string, len(registered))
				for i, s := range registered {
					names[i] = s.Name
				}
				hint := "no sources registered yet — add one with: weft source add <name> <path>"
				if len(names) > 0 {
					hint = "registered sources: " + strings.Join(names, ", ")
				}
				return fmt.Errorf("source %q not found — %s", name, hint)
			}
		}

		p := profile.Profile{
			Name:    args[0],
			Sources: names,
			Overlay: profile.Overlay(profileOverlay),
			Targets: profileTargets,
		}
		if err := newProfileManager().Create(p); err != nil {
			return err
		}

		fmt.Printf("✓ Profile %q created\n", p.Name)
		fmt.Printf("  sources:  %s\n", strings.Join(names, ", "))
		fmt.Printf("  overlay:  %s\n", p.Overlay)
		if len(p.Targets) > 0 {
			fmt.Printf("  targets:  %s\n", strings.Join(p.Targets, ", "))
		}
		fmt.Printf("\nActivate with: weft profile use %s\n", p.Name)
		return nil
	},
}

// validateOverlay returns an error if s is not a known overlay strategy.
func validateOverlay(s string) error {
	valid := []profile.Overlay{profile.OverlayCascade, profile.OverlayMerge, profile.OverlayLastWins}
	if slices.Contains(valid, profile.Overlay(s)) {
		return nil
	}
	names := make([]string, len(valid))
	for i, v := range valid {
		names[i] = string(v)
	}
	return fmt.Errorf("unknown overlay %q — valid values: %s", s, strings.Join(names, ", "))
}

// validateTarget returns an error if s is not a known harness name.
func validateTarget(s string) error {
	reg := harness.NewRegistry(harness.Instances()...)
	if _, ok := reg.Get(s); ok {
		return nil
	}
	all := harness.All()
	names := make([]string, len(all))
	for i, h := range all {
		names[i] = h.H.Name()
	}
	return fmt.Errorf("unknown target %q — valid values: %s", s, strings.Join(names, ", "))
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
		fmt.Fprintln(w, "NAME\tSOURCES\tOVERLAY\tTARGETS\tSTATUS")
		for _, p := range profiles {
			status := ""
			if active != "" && p.Name == active {
				status = "active"
			}
			targets := p.ResolvedTargets()
			targetStr := "-"
			if len(targets) > 0 {
				targetStr = strings.Join(targets, ", ")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				p.Name,
				strings.Join(p.Sources, ", "),
				p.Overlay,
				targetStr,
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

By default the command stays running and re-applies automatically whenever a
file inside any source root changes. Pass --no-watch to apply once and exit
(useful in CI or scripts).`,
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
		if resolvedTargets := p.ResolvedTargets(); len(resolvedTargets) > 0 {
			fmt.Printf("  targets: %s\n", strings.Join(resolvedTargets, ", "))
		}
		fmt.Printf("  staged: %s\n", stagedDir)

		// 6. Enter watch mode unless opted out.
		if !profileNoWatch {
			fmt.Println("\nWatching for changes... (Ctrl-C to stop)")
			var guard watch.ApplyGuard

			// Source watcher: re-apply when source files change.
			stopSrc, err := watch.Debounced(roots, 300*time.Millisecond, func() {
				fmt.Printf("\n[weft] source change detected — re-applying...\n")
				guard.Lock()
				defer guard.Unlock()
				if applyErr := mergeAndApply(p, roots, srcs, cfgDir, true); applyErr != nil {
					fmt.Fprintf(os.Stderr, "[weft] error: %v\n", applyErr)
					return
				}
				fmt.Printf("[weft] applied at %s\n", time.Now().Format("15:04:05"))
			})
			if err != nil {
				return fmt.Errorf("starting source watcher: %w", err)
			}

			// Target watchers: watch each configured target directory for external edits.
			var stopTargets []func()
			for _, tgt := range resolveApplyTargets(p, true) {
				targetRoot := harnessTargetRoot(cfgDir, tgt)
				if targetRoot == "" {
					continue
				}
				// tgt is per-iteration in Go 1.22+ (cf. Java: effectively final in lambda)
				tgt := tgt
				stopTgt, watchErr := watch.DebouncedTarget(
					[]string{targetRoot}, 300*time.Millisecond, &guard,
					func(changes []watch.TargetChange) {
						m, loadErr := manifest.Load(cfgDir, tgt)
						if loadErr != nil {
							fmt.Fprintf(os.Stderr, "[weft] loading manifest: %v\n", loadErr)
							return
						}
						for _, c := range changes {
							fmt.Printf("\n[weft] target changed: %s\n", c.Rel)
							performed, wbErr := writeBackSingleSource(m, c, p, srcs)
							if wbErr != nil {
								fmt.Fprintf(os.Stderr, "[weft] write-back error for %s: %v\n", c.Rel, wbErr)
								continue
							}
							if !performed && len(m.SourceFiles[c.Rel]) > 1 {
								performed, wbErr = writeBackMergedSource(m, c, p, srcs)
								if wbErr != nil {
									fmt.Fprintf(os.Stderr, "[weft] write-back error for %s: %v\n", c.Rel, wbErr)
									continue
								}
							}
							if performed {
								fmt.Printf("[weft] wrote %s back to source (source watcher will re-apply)\n", c.Rel)
							} else {
								fmt.Printf("[weft] %s: no owning source found — set write_back.default in profile\n", c.Rel)
							}
						}
					},
				)
				if watchErr != nil {
					stopSrc()
					for _, s := range stopTargets {
						s()
					}
					return fmt.Errorf("starting target watcher for %s: %w", tgt, watchErr)
				}
				stopTargets = append(stopTargets, stopTgt)
			}

			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			<-sig
			stopSrc()
			for _, s := range stopTargets {
				s()
			}
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

		if _, _, err := stageProfile(pA, rootsA, srcsA, dirA); err != nil {
			return fmt.Errorf("staging %q: %w", nameA, err)
		}
		if _, _, err := stageProfile(pB, rootsB, srcsB, dirB); err != nil {
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

		// Stage to a temp dir so we can run the quality report on merged content.
		tmpDir, err := os.MkdirTemp("", "weft-inspect-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()
		if _, _, stageErr := stageProfile(p, roots, srcs, tmpDir); stageErr == nil {
			fmt.Println()
			fmt.Println("Quality report:")
			printQualityReport(tmpDir, p, roots, srcs)
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
	if targets := p.ResolvedTargets(); len(targets) > 0 {
		fmt.Printf("  targets:  %s\n", strings.Join(targets, ", "))
	}
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
	profileCreateCmd.Flags().StringArrayVar(&profileTargets, "target", nil, "harness to apply to; repeat for multiple (see: weft target list)")
	_ = profileCreateCmd.MarkFlagRequired("sources")

	profileUseCmd.Flags().BoolVar(&profileNoWatch, "no-watch", false, "apply once and exit without watching for changes")

	profileInspectCmd.Flags().StringVar(&inspectFormat, "format", "text", "output format: text|mermaid")

	profileDiffCmd.Flags().BoolVarP(&diffVerbose, "verbose", "v", false, "show line-level diff for changed, added, and removed files")
}
