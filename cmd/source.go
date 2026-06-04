package cmd

import (
	"bufio"
	"fmt"
	"io"
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
			if _, err := runSync(s, os.Stdout); err != nil {
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
// Returns (true, nil) when the local tree changed, (false, nil) when already up to date.
// All progress messages are written to out.
func runSync(s source.Source, out io.Writer) (bool, error) {
	expanded := source.ExpandHome(s.Root)

	auth, err := git.ResolveAuth(s.Remote)
	if err != nil {
		return false, fmt.Errorf("resolving auth: %w", err)
	}

	// Clone if the directory does not exist yet.
	if _, err := os.Stat(expanded); os.IsNotExist(err) {
		fmt.Fprintf(out, "Cloning %s from %s...\n", s.Name, s.Remote)
		if err := git.Clone(s.Remote, expanded, s.Branch, auth, out); err != nil {
			return false, fmt.Errorf("clone failed: %w", err)
		}
		fmt.Fprintf(out, "✓ %s cloned → %s\n", s.Name, s.Root)
		return true, nil
	}

	// Path exists but isn't a repo — stop before doing anything destructive.
	if !git.IsRepo(expanded) {
		return false, fmt.Errorf("%s exists but is not a git repository\n"+
			"  remove it or point the source to a different path", s.Root)
	}

	// Pull.
	fmt.Fprintf(out, "Syncing %s (%s)...\n", s.Name, s.Root)
	repo, err := git.Open(expanded)
	if err != nil {
		return false, err
	}
	updated, err := repo.Pull(s.Branch, auth)
	if err != nil {
		return false, fmt.Errorf("pull failed: %w", err)
	}
	if updated {
		fmt.Fprintf(out, "✓ %s updated\n", s.Name)
	} else {
		fmt.Fprintf(out, "  %s already up to date\n", s.Name)
	}
	return updated, nil
}

var pushForce bool

var sourcePushCmd = &cobra.Command{
	Use:   "push <name>",
	Short: "Push local commits to the source remote",
	Long: `Push commits from the local source directory to its configured remote.

Asks for confirmation unless --force is given. Never force-pushes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := newRegistry().Get(args[0])
		if err != nil {
			return err
		}
		expanded := source.ExpandHome(s.Root)

		if !git.IsRepo(expanded) {
			return fmt.Errorf("%s is not a git repository — run 'weft source sync %s' first",
				s.Root, s.Name)
		}

		r, err := git.Open(expanded)
		if err != nil {
			return err
		}

		branch, err := r.HeadBranch()
		if err != nil {
			return fmt.Errorf("reading branch: %w", err)
		}

		fmt.Printf("Push %s  %s → %s\n", s.Name, branch, s.Remote)

		if !pushForce && !confirm("Continue? (y/N) ") {
			fmt.Println("Aborted.")
			return nil
		}

		auth, err := git.ResolveAuth(s.Remote)
		if err != nil {
			return fmt.Errorf("resolving auth: %w", err)
		}

		if err := r.Push(auth); err != nil {
			return fmt.Errorf("push failed: %w", err)
		}
		fmt.Printf("✓ %s pushed\n", s.Name)
		return nil
	},
}

// confirm prints prompt and returns true only if the user types "y".
func confirm(prompt string) bool {
	fmt.Print(prompt)
	sc := bufio.NewScanner(os.Stdin)
	sc.Scan()
	return strings.EqualFold(strings.TrimSpace(sc.Text()), "y")
}

var sourceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show git state for all registered sources",
	RunE: func(cmd *cobra.Command, args []string) error {
		sources, err := newRegistry().List()
		if err != nil {
			return err
		}
		if len(sources) == 0 {
			fmt.Println("No sources registered.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tROOT\tBRANCH\tSTATE")
		for _, s := range sources {
			branch, state := sourceState(source.ExpandHome(s.Root))
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Name, s.Root, branch, state)
		}
		return w.Flush()
	},
}

// sourceState returns the branch and a one-word state for display.
func sourceState(path string) (branch, state string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "-", "not cloned"
	}
	if !git.IsRepo(path) {
		return "-", "not a git repo"
	}
	r, err := git.Open(path)
	if err != nil {
		return "-", "error"
	}
	b, err := r.HeadBranch()
	if err != nil {
		b = "?"
	}
	clean, err := r.IsClean()
	switch {
	case err != nil:
		state = "error"
	case clean:
		state = "clean"
	default:
		state = "dirty"
	}
	return b, state
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
	sourcePushCmd.Flags().BoolVarP(&pushForce, "force", "f", false, "skip confirmation prompt")
}

// boolWord renders a bool as "yes" / "no" for display.
func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
