package cmd

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/homemove"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/source"
)

var (
	migrateDryRun bool
	migrateDocs   bool
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Relocate weft state to the ADR-0003 home layout",
	Long: `Consolidate weft into the two-home layout (ADR 0003):

  source content  -> ~/weft/sources/<name>   (each registered source, out of
                                               wherever its repo currently lives)
  audit           -> ~/.config/weft/audit     (folds in the stray ~/.weft/audit)

Migration is non-destructive: content is moved (never deleted), a populated
destination is refused rather than clobbered, and a symlink bridge is left at the
old path so existing absolute references keep resolving. Re-running is a no-op.
The registry and profile definitions stay in the engine room (~/.config/weft) —
only bulky content moves.

Add --docs to also consolidate ~/docs under ~/weft/docs (see 'weft docs adopt').`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		home := weftHomeDir()
		if home == "" {
			return fmt.Errorf("cannot resolve weft home directory")
		}

		var problems int

		// Relocate each registered source's content into ~/weft/sources/<name>.
		reg, err := newRegistry()
		if err != nil {
			return err
		}
		srcs, err := reg.List()
		if err != nil {
			return fmt.Errorf("listing sources: %w", err)
		}
		for _, s := range srcs {
			dst := filepath.Join(home, "sources", s.Name)
			if err := migrateSource(out, reg, s.Name, locate.ExpandHome(s.Root), dst); err != nil {
				fmt.Fprintf(out, "  ! %s: %v\n", s.Name, err)
				problems++
			}
		}

		// Fold the machine-wide ~/.weft/audit. This is a global operation, so skip
		// it under a custom --config (which is fully isolated — never reach into the
		// real HOME from an isolated run).
		if cfgFile == "" {
			if err := migrateAudit(out, legacyGlobalAuditDir(), filepath.Join(configDir(), "audit")); err != nil {
				fmt.Fprintf(out, "  ! audit: %v\n", err)
				problems++
			}
		}

		if migrateDocs {
			if err := adoptDocs(out, migrateDryRun); err != nil {
				fmt.Fprintf(out, "  ! docs: %v\n", err)
				problems++
			}
		}

		if problems > 0 {
			return fmt.Errorf("%d item(s) needed attention — see above", problems)
		}
		if migrateDryRun {
			fmt.Fprintln(out, "dry run complete — no changes made.")
		} else {
			fmt.Fprintln(out, "migration complete.")
		}
		return nil
	},
}

// migrateSource relocates one registered source's content into the workbench,
// honouring --dry-run.
func migrateSource(out io.Writer, reg *source.FileRegistry, name, src, dst string) error {
	if migrateDryRun {
		switch {
		case src == dst:
			fmt.Fprintf(out, "  source %-12s already at %s\n", name, dst)
		case dirExists(src):
			fmt.Fprintf(out, "  source %-12s would relocate %s -> %s\n", name, src, dst)
		default:
			fmt.Fprintf(out, "  source %-12s nothing to move (%s absent)\n", name, src)
		}
		return nil
	}
	res, err := relocateSource(reg, name, dst)
	if err != nil {
		return err
	}
	if res.Moved {
		bridged := ""
		if res.Bridged {
			bridged = fmt.Sprintf(" (bridge at %s)", src)
		}
		fmt.Fprintf(out, "  source %-12s relocated -> %s%s\n", name, dst, bridged)
	} else {
		fmt.Fprintf(out, "  source %-12s %s\n", name, res.SkipReason)
	}
	return nil
}

// migrateAudit folds the legacy global audit dir into the engine-room location.
func migrateAudit(out io.Writer, src, dst string) error {
	if migrateDryRun {
		switch {
		case src == dst:
			fmt.Fprintf(out, "  audit        already at %s\n", dst)
		case dirExists(src):
			fmt.Fprintf(out, "  audit        would move %s -> %s\n", src, dst)
		default:
			fmt.Fprintf(out, "  audit        nothing to move (%s absent)\n", src)
		}
		return nil
	}
	res, err := homemove.Move(src, dst, false)
	if err != nil {
		return err
	}
	if res.Moved {
		fmt.Fprintf(out, "  audit        moved -> %s\n", dst)
	} else {
		fmt.Fprintf(out, "  audit        %s\n", res.SkipReason)
	}
	return nil
}

// adoptDocs consolidates the docs home under ~/weft/docs and repoints docs_dir.
// It backs nothing up because it moves (never deletes) and leaves a bridge
// symlink at the old ~/docs. Idempotent.
func adoptDocs(out io.Writer, dryRun bool) error {
	home := weftHomeDir()
	if home == "" {
		return fmt.Errorf("cannot resolve weft home directory")
	}
	src := docsDir()
	dst := filepath.Join(home, "docs")

	if src == dst {
		fmt.Fprintf(out, "  docs      already adopted at %s\n", dst)
		return nil
	}
	if dryRun {
		if dirExists(src) {
			fmt.Fprintf(out, "  docs      would move %s -> %s and set docs_dir\n", src, dst)
		} else {
			fmt.Fprintf(out, "  docs      would create %s and set docs_dir\n", dst)
		}
		return nil
	}

	res, err := homemove.Move(src, dst, true)
	if err != nil {
		return err
	}
	if res.Moved {
		bridged := ""
		if res.Bridged {
			bridged = fmt.Sprintf(" (bridge left at %s)", src)
		}
		fmt.Fprintf(out, "  docs      moved -> %s%s\n", dst, bridged)
	} else {
		// Nothing to move (no ~/docs yet): still create the adopted home so the
		// pointer is valid.
		if err := ensureDir(dst); err != nil {
			return err
		}
		fmt.Fprintf(out, "  docs      home set to %s\n", dst)
	}
	if err := config.SetPath("docs_dir", dst); err != nil {
		return fmt.Errorf("adopted, but failed to persist docs_dir: %w", err)
	}
	return nil
}

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Manage the weft docs home",
}

var docsAdoptCmd = &cobra.Command{
	Use:   "adopt",
	Short: "Consolidate ~/docs under ~/weft/docs and repoint {{weft.docs}}",
	Long: `Move the docs home under the weft workbench (~/weft/docs) and set docs_dir
so {{weft.docs}} resolves there. A symlink bridge is left at the old ~/docs, so
existing references keep working. Non-destructive and idempotent.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return adoptDocs(cmd.OutOrStdout(), false)
	},
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "show what would move without changing anything")
	migrateCmd.Flags().BoolVar(&migrateDocs, "docs", false, "also consolidate ~/docs under ~/weft/docs")
	rootCmd.AddCommand(migrateCmd)
	docsCmd.AddCommand(docsAdoptCmd)
	rootCmd.AddCommand(docsCmd)
}
