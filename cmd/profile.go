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

	"github.com/jophira/weft/internal/config"
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
)

// mergeAndApply runs the merge+apply pipeline for a resolved profile.
// quiet suppresses informational output (used during watch re-applies).
func mergeAndApply(p *profile.Profile, roots []string, srcs []source.Source, cfgDir string, quiet bool) error {
	stagedDir := filepath.Join(cfgDir, "staged", p.Name)
	if err := os.RemoveAll(stagedDir); err != nil {
		return fmt.Errorf("clearing staged dir: %w", err)
	}

	if !quiet {
		fmt.Printf("Merging %d source(s) [%s] with strategy %q...\n",
			len(roots), strings.Join(p.Sources, ", "), p.Overlay)
	}
	manifest, err := merge.New(p.Overlay).
		WithFilter(managedFilter(srcs)).
		MergeRoots(roots, stagedDir)
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
	if err := h.Apply(stagedDir); err != nil {
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

		// 1. Load the profile.
		p, err := newProfileManager().Get(name)
		if err != nil {
			return err
		}

		// 2. Resolve source roots, verify each exists on disk.
		reg := newRegistry()
		var roots []string
		var srcs []source.Source
		for _, srcName := range p.Sources {
			s, err := reg.Get(srcName)
			if err != nil {
				return fmt.Errorf("source %q referenced by profile not found: %w", srcName, err)
			}
			expanded := source.ExpandHome(s.Root)
			if _, err := os.Stat(expanded); err != nil {
				return fmt.Errorf(
					"source %q root %s does not exist — clone or create it first",
					s.Name, s.Root,
				)
			}
			roots = append(roots, expanded)
			srcs = append(srcs, *s)
		}

		// 3. Resolve config dir (used for staging).
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

var profileDiffCmd = &cobra.Command{
	Use:   "diff <profile-a> <profile-b>",
	Short: "Show what changes when switching from one profile to another",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// TODO: implement file-level diff between merged profiles
		fmt.Printf("profile diff — not yet implemented\n")
		return nil
	},
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
		profileDiffCmd,
		profileDeleteCmd,
	)

	profileCreateCmd.Flags().StringVar(&profileSources, "sources", "", "comma-separated source names (required)")
	profileCreateCmd.Flags().StringVar(&profileOverlay, "overlay", "cascade", "cascade|merge|last-wins")
	profileCreateCmd.Flags().StringVar(&profileTarget, "target", "", "default harness: claude-code|cursor|warp")
	_ = profileCreateCmd.MarkFlagRequired("sources")

	profileUseCmd.Flags().BoolVar(&profileWatch, "watch", false, "re-apply automatically when source files change")
}
