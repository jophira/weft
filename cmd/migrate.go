package cmd

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/homemove"
)

var (
	migrateDryRun bool
	migrateDocs   bool
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Relocate weft state to the ADR-0003 home layout",
	Long: `Consolidate weft's sprawled state into the two-home layout (ADR 0003):

  sources   -> ~/weft/sources        (out of the hidden ~/.config dotfile)
  profiles  -> ~/weft/profiles
  audit     -> ~/.config/weft/audit   (folds in the stray ~/.weft/audit)

Migration is non-destructive: content is moved (never deleted), a populated
destination is refused rather than clobbered, and a symlink bridge is left at the
old path so existing absolute references keep resolving. Re-running is a no-op.

Add --docs to also consolidate ~/docs under ~/weft/docs (see 'weft docs adopt').`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		home := weftHomeDir()
		if home == "" {
			return fmt.Errorf("cannot resolve weft home directory")
		}

		// Folding the machine-wide ~/.weft/audit is a global operation. Under a
		// custom --config weft is fully isolated, so skip it — never reach into the
		// real HOME from an isolated run.
		auditSrc := ""
		if cfgFile == "" {
			auditSrc = legacyGlobalAuditDir()
		}
		moves := []struct {
			label, src, dst, configKey string
			bridge                     bool
		}{
			{"sources", currentSourcesDir(), filepath.Join(home, "sources"), "sources_dir", true},
			{"profiles", currentProfilesDir(), filepath.Join(home, "profiles"), "profiles_dir", true},
			{"audit", auditSrc, filepath.Join(configDir(), "audit"), "", false},
		}

		var problems int
		for _, m := range moves {
			if err := migrateOne(out, m.label, m.src, m.dst, m.bridge, m.configKey); err != nil {
				fmt.Fprintf(out, "  ! %s: %v\n", m.label, err)
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

// migrateOne relocates one item and, on a real (non-dry-run) move, repoints the
// given config key at the destination so future runs resolve there directly.
func migrateOne(out io.Writer, label, src, dst string, bridge bool, configKey string) error {
	if src == "" {
		return nil
	}
	if migrateDryRun {
		switch {
		case src == dst:
			fmt.Fprintf(out, "  %-9s already at %s\n", label, dst)
		case dirExists(src):
			fmt.Fprintf(out, "  %-9s would move %s -> %s\n", label, src, dst)
		default:
			fmt.Fprintf(out, "  %-9s nothing to move (%s absent)\n", label, src)
		}
		return nil
	}

	res, err := homemove.Move(src, dst, bridge)
	if err != nil {
		return err
	}
	switch {
	case res.Moved:
		bridged := ""
		if res.Bridged {
			bridged = fmt.Sprintf(" (bridge left at %s)", src)
		}
		fmt.Fprintf(out, "  %-9s moved -> %s%s\n", label, dst, bridged)
		if configKey != "" {
			if err := config.SetPath(configKey, dst); err != nil {
				return fmt.Errorf("moved, but failed to persist %s: %w", configKey, err)
			}
		}
	default:
		fmt.Fprintf(out, "  %-9s %s\n", label, res.SkipReason)
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
