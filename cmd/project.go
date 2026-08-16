package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/project"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Inspect the repositories weft delivers project rules to",
	Long: `Weft records every repository you run it in, so project-scoped rules can be
delivered without the watcher having to guess or the disk having to be scanned.

Registration happens on its own: any weft command run inside a repository adds
it, including the session hook and a status line. Entries whose directory has
gone, and entries not visited for a while, are dropped automatically on the next
registration, so this list never needs gardening.`,
}

var projectListAll bool

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show the registered repositories",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		reg, err := loadProjectRegistry()
		if err != nil {
			return err
		}
		if len(reg.Projects) == 0 {
			fmt.Fprintln(out, "No repositories registered yet.")
			fmt.Fprintln(out, "  run any weft command inside a repository and it will appear here")
			return nil
		}

		now := time.Now().UTC()
		maxAge := projectMaxAge()
		w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "REPO\tLAST SEEN\tSTATE\tROOT")
		for _, p := range reg.Projects {
			state := "active"
			switch {
			case !p.Enabled:
				state = "disabled"
			case p.Stale(now, maxAge):
				state = "stale"
			}
			if state == "stale" && !projectListAll {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Repo, humaniseSince(now, p.LastSeen), state, p.Root)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if !projectListAll {
			fmt.Fprintln(out, "\n  --all also shows entries past the staleness window")
		}
		return nil
	},
}

var projectStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show what weft knows about the current repository",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolving working directory: %w", err)
		}
		root, ok := project.FindRoot(cwd)
		if !ok {
			fmt.Fprintf(out, "%s is not inside a git repository.\n", cwd)
			fmt.Fprintln(out, "  project rules apply per repository, so there is nothing to report here")
			return nil
		}

		repo, remote := project.Identity(root)
		fmt.Fprintf(out, "Repository: %s\n", repo)
		fmt.Fprintf(out, "  root:    %s\n", root)
		if remote != "" {
			fmt.Fprintf(out, "  remote:  %s\n", remote)
		} else {
			fmt.Fprintln(out, "  remote:  (none)")
		}

		reg, err := loadProjectRegistry()
		if err != nil {
			return err
		}
		if p := reg.Get(root); p != nil {
			fmt.Fprintf(out, "  tracked: yes, last seen %s\n", humaniseSince(time.Now().UTC(), p.LastSeen))
			if p.Profile != "" {
				fmt.Fprintf(out, "  profile: %s\n", p.Profile)
			}
		} else {
			// Registration happens in PersistentPreRun, so reaching this branch
			// means tracking is switched off rather than simply not yet done.
			fmt.Fprintln(out, "  tracked: no (project_sync is off)")
		}

		stateDir := filepath.Join(root, project.StateDirName)
		if info, statErr := os.Stat(stateDir); statErr == nil && info.IsDir() {
			fmt.Fprintf(out, "  state:   %s\n", stateDir)
		} else {
			fmt.Fprintln(out, "  state:   (not written yet)")
		}
		return nil
	},
}

var projectForgetStale bool

var projectForgetCmd = &cobra.Command{
	Use:   "forget [root]",
	Short: "Drop a repository from the registry",
	Long: `Remove one repository, or with --stale every entry past the staleness window.

Neither is normally necessary: pruning runs on every registration. This exists
for the case where you want an entry gone now rather than on the next visit.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		cfgDir := configDir()
		if cfgDir == "" {
			return fmt.Errorf("resolving config directory")
		}
		reg, err := project.Load(cfgDir)
		if err != nil {
			return err
		}

		if projectForgetStale {
			dropped := reg.Prune(time.Now().UTC(), projectMaxAge(), project.DirExists)
			if len(dropped) == 0 {
				fmt.Fprintln(out, "Nothing stale to forget.")
				return nil
			}
			for _, p := range dropped {
				fmt.Fprintf(out, "  - %s (%s)\n", p.Repo, p.Root)
			}
			if err := project.Save(cfgDir, reg); err != nil {
				return err
			}
			fmt.Fprintf(out, "✓ forgot %d entry(ies)\n", len(dropped))
			return nil
		}

		if len(args) == 0 {
			return fmt.Errorf("give a repository root, or use --stale")
		}
		root, err := expandAndAbs(args[0])
		if err != nil {
			return fmt.Errorf("resolving path: %w", err)
		}
		if !reg.Forget(root) {
			return fmt.Errorf("%s is not registered — run 'weft project list' to see what is", root)
		}
		if err := project.Save(cfgDir, reg); err != nil {
			return err
		}
		fmt.Fprintf(out, "✓ forgot %s\n", root)
		return nil
	},
}

// loadProjectRegistry reads the registry, turning a missing config dir into a
// clear error rather than an empty list that would read as "nothing tracked".
func loadProjectRegistry() (*project.Registry, error) {
	cfgDir := configDir()
	if cfgDir == "" {
		return nil, fmt.Errorf("resolving config directory")
	}
	return project.Load(cfgDir)
}

// humaniseSince renders a timestamp as a rough age, which is what the reader
// actually wants to know when scanning a list of repositories.
func humaniseSince(now, then time.Time) string {
	if then.IsZero() {
		return "never"
	}
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func init() {
	rootCmd.AddCommand(projectCmd)
	projectCmd.AddCommand(projectListCmd, projectStatusCmd, projectForgetCmd)

	projectListCmd.Flags().BoolVar(&projectListAll, "all", false, "include entries past the staleness window")
	projectForgetCmd.Flags().BoolVar(&projectForgetStale, "stale", false, "forget every entry past the staleness window")
}
