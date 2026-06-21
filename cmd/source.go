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
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/sourcesync"
)

// newRegistry builds a FileRegistry using the configured sources directory,
// falling back to the default path when no config file is present.
func newRegistry() (*source.FileRegistry, error) {
	dir := viper.GetString("sources_dir")
	if dir == "" {
		cfg, err := config.Defaults()
		if err != nil {
			return nil, err
		}
		dir = cfg.SourcesDir
	}
	return source.NewFileRegistry(dir), nil
}

// ── Flags ─────────────────────────────────────────────────────────────────────

var (
	addBranch          string
	addAutoPull        bool
	addInstructionGlob string
	addRemote          string
	addProjectDirNames string
	addPriority        int
)

// ── Commands ──────────────────────────────────────────────────────────────────

var sourceCmd = &cobra.Command{
	Use:   "source",
	Short: "Manage AI rule sources",
}

var sourceAddCmd = &cobra.Command{
	Use:   "add <name> <path>",
	Short: "Register a new rule source",
	Long: `Register a local directory of AI rules as a named source.

  <name>  identifier, e.g. "work" or "personal" (lowercase, no spaces)
  <path>  local root directory, e.g. ~/.claude or ~/my-rules

The git remote is resolved automatically:
  - If --remote is given it is used as-is.
  - Else if <path> is already a git repo, the origin remote is read from it.
  - Else the source is registered without a remote (local-only; sync is a no-op).

If --remote is given and <path> is already a git repo whose origin differs,
the command errors rather than silently tracking a mismatched URL.

--instruction-glob controls which files are assembled into the instruction
context when this source is merged. The default "CLAUDE.md" reads only the
root-level file. Use "**/*.md" to assemble a full domain hierarchy
(Backend/BACKEND.md, Frontend/FRONTEND.md, etc.) in parent-before-child order.
Managed subdirectory files (commands/, skills/, etc.) are always excluded.

--project-dir-names controls which directories are treated as project-rule
roots. Weft walks the entire source tree and treats any directory whose base
name matches one of these names as a project root. All .md files found inside
(including nested subdirectories, up to 10 levels deep) are listed in the
assembled CLAUDE.md under a "<!-- weft:projects -->" placeholder.

  Default names: projects, project-rules

  Flat layout — one file per project:
    php/project-rules/ubs-keyinvest.md
    java/project-rules/instrument-service.md

  Nested layout — one subdirectory per project:
    php/project-rules/ubs-keyinvest/ubs-keyinvest.md
    java/project-rules/instrument-service/instrument-service.md

Both layouts are supported. To add the project snippet to your CLAUDE.md,
place the marker <!-- weft:projects --> where the list should appear.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, rawPath := args[0], args[1]
		expanded := locate.ExpandHome(rawPath)

		remote := addRemote

		// Infer or validate the remote from the repo at <path>.
		if git.IsRepo(expanded) {
			repo, err := git.Open(expanded)
			if err != nil {
				return err
			}
			repoRemote, err := repo.OriginRemote()
			if err != nil {
				return err
			}
			switch {
			case remote == "":
				// Infer from repo.
				remote = repoRemote
			case repoRemote != "" && remote != repoRemote:
				return fmt.Errorf(
					"--remote %q does not match the repo's existing origin %q\n"+
						"  drop --remote to use the repo's remote, or re-clone at a different path",
					remote, repoRemote,
				)
			}
		}

		structure := source.DefaultStructure()
		structure.InstructionGlob = addInstructionGlob
		if addProjectDirNames != "" {
			structure.ProjectDirNames = parseSources(addProjectDirNames)
		}

		s := source.Source{
			Name:      name,
			Root:      rawPath,
			Priority:  addPriority,
			Remote:    remote,
			Branch:    addBranch,
			AutoPull:  addAutoPull,
			AutoPush:  false,
			Structure: structure,
		}
		reg, err := newRegistry()
		if err != nil {
			return err
		}
		if err := reg.Add(s); err != nil {
			return err
		}

		// Re-read what was saved so we display the normalised path.
		saved, err := reg.Get(s.Name)
		if err != nil {
			return err
		}

		remoteDisplay := saved.Remote
		if remoteDisplay == "" {
			remoteDisplay = "(none — local only)"
		}

		fmt.Printf("✓ Source %q registered\n", saved.Name)
		fmt.Printf("  root:               %s\n", saved.Root)
		fmt.Printf("  priority:           %d\n", saved.Priority)
		fmt.Printf("  remote:             %s\n", remoteDisplay)
		fmt.Printf("  branch:             %s\n", saved.Branch)
		fmt.Printf("  auto-pull:          %v\n", boolWord(saved.AutoPull))
		fmt.Printf("  instruction-glob:   %s\n", saved.Structure.InstructionGlob)
		fmt.Printf("  project-dir-names:  %s\n", strings.Join(saved.Structure.EffectiveProjectDirNames(), ", "))

		// Warn if the root path does not exist yet.
		if _, err := os.Stat(expanded); os.IsNotExist(err) {
			fmt.Printf("\n  ⚠  %s does not exist yet\n", saved.Root)
			fmt.Printf("     Clone or create it with: weft source sync %s\n", saved.Name)
		}
		return nil
	},
}

var sourceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered sources",
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := newRegistry()
		if err != nil {
			return err
		}
		sources, err := reg.List()
		if err != nil {
			return err
		}
		if len(sources) == 0 {
			fmt.Println("No sources registered.")
			fmt.Println("Add one with: weft source add <name> <path>")
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
		reg, err := newRegistry()
		if err != nil {
			return err
		}
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

// runSync clones or pulls a single source, writing progress to out.
// Returns (true, nil) when the local tree changed, (false, nil) when already up to date.
// Pass io.Discard as out to suppress all progress messages (e.g. background auto-sync).
//
// cf. Java: static utility method — no receiver, pure function on the Source value.
func runSync(s source.Source, out io.Writer) (bool, error) {
	return sourcesync.SyncSource(s, out)
}

var (
	pushForce   bool
	pushMessage string
)

var sourcePushCmd = &cobra.Command{
	Use:   "push <name>",
	Short: "Push local commits to the source remote",
	Long: `Push commits from the local source directory to its configured remote.

If the working tree is dirty and --message is given, all changes are staged
and committed before pushing. Without --message, a dirty tree aborts with a
hint. --force skips the confirmation prompt but does not auto-commit.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := newRegistry()
		if err != nil {
			return err
		}
		s, err := reg.Get(args[0])
		if err != nil {
			return err
		}
		expanded := locate.ExpandHome(s.Root)

		if !git.IsRepo(expanded) {
			return fmt.Errorf("%s is not a git repository — run 'weft source sync %s' first",
				s.Root, s.Name)
		}

		r, err := git.Open(expanded)
		if err != nil {
			return err
		}

		// Check for uncommitted changes.
		clean, err := r.IsClean()
		if err != nil {
			return fmt.Errorf("checking working tree: %w", err)
		}
		if !clean {
			if pushMessage == "" {
				return fmt.Errorf(
					"%s has uncommitted changes\n"+
						"  commit first:  cd %s && git commit -am \"your message\"\n"+
						"  or let weft commit: weft source push %s --message \"your message\"",
					s.Name, expanded, s.Name,
				)
			}
			fmt.Printf("Committing changes in %s...\n", s.Name)
			if err := r.CommitAll(pushMessage); err != nil {
				return fmt.Errorf("commit failed: %w", err)
			}
			fmt.Printf("  ✓ committed: %s\n", pushMessage)
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
		reg, err := newRegistry()
		if err != nil {
			return err
		}
		sources, err := reg.List()
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
			branch, state := sourceState(locate.ExpandHome(s.Root))
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
		reg, err := newRegistry()
		if err != nil {
			return err
		}
		if err := reg.Remove(args[0]); err != nil {
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

	sourceAddCmd.Flags().StringVar(&addRemote, "remote", "", "git remote URL (inferred from repo origin when omitted)")
	sourceAddCmd.Flags().StringVar(&addBranch, "branch", "main", "branch to track")
	sourceAddCmd.Flags().BoolVar(&addAutoPull, "auto-pull", true, "pull on 'weft source sync'")
	sourceAddCmd.Flags().StringVar(&addInstructionGlob, "instruction-glob", source.DefaultStructure().InstructionGlob, `glob pattern for instruction files: "CLAUDE.md" (root only) or "**/*.md" (full hierarchy)`)
	sourceAddCmd.Flags().StringVar(&addProjectDirNames, "project-dir-names", "", `comma-separated directory names to search anywhere in the source tree for project rule files (default: "projects,project-rules")`)
	sourceAddCmd.Flags().IntVar(&addPriority, "priority", 0, "layering priority: higher numbers win on conflict (applied later); unset = 0 (lowest)")
	sourcePushCmd.Flags().BoolVarP(&pushForce, "force", "f", false, "skip confirmation prompt")
	sourcePushCmd.Flags().StringVarP(&pushMessage, "message", "m", "", "stage all changes, commit with this message, then push")
}

// boolWord renders a bool as "yes" / "no" for display.
func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
