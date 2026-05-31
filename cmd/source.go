package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/git"
	"github.com/jophira/weft/internal/source"
)

// newRegistry builds a FileRegistry using the configured sources directory,
// falling back to the default path when no config file is present.
func newRegistry() *source.FileRegistry {
	dir := viper.GetString("sources_dir")
	if dir == "" {
		cfg, _ := config.Defaults()
		dir = cfg.SourcesDir
	}
	return source.NewFileRegistry(dir)
}

// ── Flags ─────────────────────────────────────────────────────────────────────

var (
	addBranch   string
	addAutoPull bool
)

// ── Commands ──────────────────────────────────────────────────────────────────

var sourceCmd = &cobra.Command{
	Use:   "source",
	Short: "Manage AI rule sources",
}

var sourceAddCmd = &cobra.Command{
	Use:   "add <name> <path> <remote>",
	Short: "Register a new rule source",
	Long: `Register a local directory of AI rules as a named source.

  <name>    identifier, e.g. "work" or "personal" (lowercase, no spaces)
  <path>    local root directory, e.g. ~/.claude or ~/my-rules
  <remote>  git remote URL, e.g. git@github.com:you/ai-rules.git`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := source.Source{
			Name:      args[0],
			Root:      args[1],
			Remote:    args[2],
			Branch:    addBranch,
			AutoPull:  addAutoPull,
			AutoPush:  false,
			Structure: source.DefaultStructure(),
		}
		reg := newRegistry()
		if err := reg.Add(s); err != nil {
			return err
		}

		// Re-read what was saved so we display the normalised path.
		saved, err := reg.Get(s.Name)
		if err != nil {
			return err
		}

		fmt.Printf("✓ Source %q registered\n", saved.Name)
		fmt.Printf("  root:      %s\n", saved.Root)
		fmt.Printf("  remote:    %s\n", saved.Remote)
		fmt.Printf("  branch:    %s\n", saved.Branch)
		fmt.Printf("  auto-pull: %v\n", boolWord(saved.AutoPull))

		// Warn if the root path does not exist yet.
		if _, err := os.Stat(args[1]); os.IsNotExist(err) {
			fmt.Printf("\n  ⚠  %s does not exist yet\n", saved.Root)
			fmt.Printf("     Clone or create it before running 'weft source sync %s'\n", saved.Name)
		}
		return nil
	},
}

var sourceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered sources",
	RunE: func(cmd *cobra.Command, args []string) error {
		sources, err := newRegistry().List()
		if err != nil {
			return err
		}
		if len(sources) == 0 {
			fmt.Println("No sources registered.")
			fmt.Println("Add one with: weft source add <name> <path> <remote>")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tROOT\tREMOTE\tBRANCH\tAUTO-PULL")
		for _, s := range sources {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				s.Name, s.Root, s.Remote, s.Branch, boolWord(s.AutoPull))
		}
		return w.Flush()
	},
}

var sourceSyncCmd = &cobra.Command{
	Use:   "sync [name]",
	Short: "Pull latest from source remote",
	Long: `Pull the latest commits from each source's git remote.

Without a name: syncs every source where auto_pull is true.
With a name:    syncs that source regardless of auto_pull.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reg := newRegistry()
		all, err := reg.List()
		if err != nil {
			return err
		}
		if len(all) == 0 {
			fmt.Println("No sources registered.")
			return nil
		}

		var toSync []source.Source
		if len(args) > 0 {
			s, err := reg.Get(args[0])
			if err != nil {
				return err
			}
			toSync = []source.Source{*s}
		} else {
			for _, s := range all {
				if s.AutoPull {
					toSync = append(toSync, s)
				}
			}
			if len(toSync) == 0 {
				fmt.Println("No sources have auto_pull enabled.")
				fmt.Println("Sync a specific source with: weft source sync <name>")
				return nil
			}
		}

		var failures []string
		for _, s := range toSync {
			if err := runSync(s); err != nil {
				failures = append(failures, fmt.Sprintf("  %s: %v", s.Name, err))
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("sync completed with errors:\n%s", strings.Join(failures, "\n"))
		}
		return nil
	},
}

// runSync clones or pulls a single source.
func runSync(s source.Source) error {
	expanded := source.ExpandHome(s.Root)

	auth, err := git.ResolveAuth(s.Remote)
	if err != nil {
		return fmt.Errorf("resolving auth: %w", err)
	}

	// Clone if the directory does not exist yet.
	if _, err := os.Stat(expanded); os.IsNotExist(err) {
		fmt.Printf("Cloning %s from %s...\n", s.Name, s.Remote)
		if err := git.Clone(s.Remote, expanded, s.Branch, auth); err != nil {
			return fmt.Errorf("clone failed: %w", err)
		}
		fmt.Printf("✓ %s cloned → %s\n", s.Name, s.Root)
		return nil
	}

	// Path exists but isn't a repo — stop before doing anything destructive.
	if !git.IsRepo(expanded) {
		return fmt.Errorf("%s exists but is not a git repository\n"+
			"  remove it or point the source to a different path", s.Root)
	}

	// Pull.
	fmt.Printf("Syncing %s (%s)...\n", s.Name, s.Root)
	repo, err := git.Open(expanded)
	if err != nil {
		return err
	}
	updated, err := repo.Pull(s.Branch, auth)
	if err != nil {
		return fmt.Errorf("pull failed: %w", err)
	}
	if updated {
		fmt.Printf("✓ %s updated\n", s.Name)
	} else {
		fmt.Printf("  %s already up to date\n", s.Name)
	}
	return nil
}

var sourcePushCmd = &cobra.Command{
	Use:   "push <name>",
	Short: "Push source to its remote (asks for confirmation)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// TODO: implement via internal/git
		fmt.Printf("push %s — not yet implemented\n", args[0])
		return nil
	},
}

var sourceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show git status for all sources",
	RunE: func(cmd *cobra.Command, args []string) error {
		// TODO: implement via internal/git
		fmt.Println("status — not yet implemented")
		return nil
	},
}

var sourceRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Deregister a source (does not delete local files or remote)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := newRegistry().Remove(args[0]); err != nil {
			return err
		}
		fmt.Printf("✓ Source %q removed\n", args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(sourceCmd)
	sourceCmd.AddCommand(
		sourceAddCmd,
		sourceListCmd,
		sourceSyncCmd,
		sourcePushCmd,
		sourceStatusCmd,
		sourceRemoveCmd,
	)

	sourceAddCmd.Flags().StringVar(&addBranch, "branch", "main", "branch to track")
	sourceAddCmd.Flags().BoolVar(&addAutoPull, "auto-pull", true, "pull on 'weft source sync'")
}

// boolWord renders a bool as "yes" / "no" for display.
func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
