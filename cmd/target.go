package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/manifest"
)

var targetCmd = &cobra.Command{
	Use:   "target",
	Short: "Manage AI harness targets",
}

var targetListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show all known harnesses and whether they are installed",
	RunE: func(cmd *cobra.Command, args []string) error {
		all := harness.All()

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tDETECTED\tCONFIG")
		for _, k := range all {
			detected := "✗"
			if k.H.Detect() {
				detected = "✓"
			}
			configPath := k.ConfigPath
			if cp, ok := k.H.(harness.ConfigPather); ok {
				configPath = cp.ConfigPath()
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", k.H.Name(), detected, configPath)
		}
		return w.Flush()
	},
}

var targetApplyCmd = &cobra.Command{
	Use:   "apply <harness>",
	Short: "Re-apply the active profile to a specific harness",
	Long: `Copy the already-merged staged output into the named harness config directory.

This re-runs the apply step without re-merging sources. Useful when you want
to apply the current profile to a second harness, or recover from a failed apply.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		activeName := viper.GetString("active_profile")
		if activeName == "" {
			return fmt.Errorf("no active profile — run 'weft profile use <name>' first")
		}

		cfgDir := configDir()
		if cfgDir == "" {
			return fmt.Errorf("resolving config directory")
		}
		stagedDir := filepath.Join(cfgDir, "staged", activeName)
		if _, err := os.Stat(stagedDir); err != nil {
			return fmt.Errorf(
				"staged output for profile %q not found\n"+
					"  run 'weft profile use %s' to merge and stage first",
				activeName, activeName,
			)
		}

		reg := harness.NewRegistry(harness.Instances()...)
		h, ok := reg.Get(args[0])
		if !ok {
			return fmt.Errorf(
				"unknown harness %q\n  run 'weft target list' to see supported harnesses",
				args[0],
			)
		}

		fmt.Printf("Applying profile %q → %s...\n", activeName, args[0])
		if err := h.Apply(stagedDir, harness.ApplyCtx{ProfileName: activeName, CfgDir: cfgDir}); err != nil {
			return fmt.Errorf("apply failed: %w", err)
		}
		fmt.Printf("✓ Applied\n")
		return nil
	},
}

var targetBackupsCmd = &cobra.Command{
	Use:   "backups <harness>",
	Short: "List available backups for a harness",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		harnessName := args[0]
		cfgDir := configDir()
		if cfgDir == "" {
			return fmt.Errorf("resolving config directory")
		}
		backupsDir := filepath.Join(cfgDir, "backups", harnessName)
		entries, err := os.ReadDir(backupsDir)
		if os.IsNotExist(err) {
			fmt.Printf("No backups found for %s.\n", harnessName)
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading backups dir: %w", err)
		}

		// Filter to directories only and sort chronologically (names are timestamps).
		var dirs []os.DirEntry
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, e)
			}
		}
		if len(dirs) == 0 {
			fmt.Printf("No backups found for %s.\n", harnessName)
			return nil
		}
		sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "TIMESTAMP\tFILES\tPATH")
		for _, d := range dirs {
			count := countFiles(filepath.Join(backupsDir, d.Name()))
			fmt.Fprintf(w, "%s\t%d\t%s\n",
				d.Name(), count,
				filepath.Join(backupsDir, d.Name()))
		}
		return w.Flush()
	},
}

var revertBackup string

var targetRevertCmd = &cobra.Command{
	Use:   "revert <harness>",
	Short: "Restore the most recent backup for a harness",
	Long: `Copy backed-up files back to the harness config directory,
restoring them to the state they were in before weft overwrote them.

By default the most recent backup is used. Use --backup to pick a specific one.
After reverting, the restored files are removed from the weft manifest so that
the next apply will treat them as externally owned.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		harnessName := args[0]
		cfgDir := configDir()
		if cfgDir == "" {
			return fmt.Errorf("resolving config directory")
		}

		// Load manifest to find targetRoot.
		m, err := manifest.Load(cfgDir, harnessName)
		if err != nil {
			return fmt.Errorf("loading manifest: %w", err)
		}
		if m.TargetRoot == "" {
			return fmt.Errorf("no manifest found for %s — nothing has been applied yet", harnessName)
		}

		// Resolve backup directory.
		backupsBase := filepath.Join(cfgDir, "backups", harnessName)
		backupDir, err := resolveBackupDir(backupsBase, revertBackup)
		if err != nil {
			return err
		}

		// Collect files to restore.
		var files []string
		if err := filepath.WalkDir(backupDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(backupDir, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
			return nil
		}); err != nil {
			return fmt.Errorf("reading backup: %w", err)
		}
		if len(files) == 0 {
			fmt.Println("Backup is empty — nothing to restore.")
			return nil
		}

		ts := filepath.Base(backupDir)
		fmt.Printf("Reverting %s from backup %s...\n", harnessName, ts)
		for _, rel := range files {
			src := filepath.Join(backupDir, rel)
			dst := filepath.Join(m.TargetRoot, rel)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("creating parent dir for %s: %w", rel, err)
			}
			data, err := os.ReadFile(src)
			if err != nil {
				return fmt.Errorf("reading backup file %s: %w", rel, err)
			}
			if err := os.WriteFile(dst, data, 0o644); err != nil { //nolint:gosec // dst is resolved from manifest target root, not user input
				return fmt.Errorf("restoring %s: %w", rel, err)
			}
			fmt.Printf("  ✓ %s\n", rel)
			delete(m.Files, rel)
		}

		if err := manifest.Save(cfgDir, m); err != nil {
			return fmt.Errorf("updating manifest: %w", err)
		}
		fmt.Printf("✓ Reverted %d file(s)\n", len(files))
		return nil
	},
}

// resolveBackupDir returns the backup directory to use.
// If timestamp is non-empty it looks for that exact dir; otherwise it picks the latest.
func resolveBackupDir(backupsBase, timestamp string) (string, error) {
	if timestamp != "" {
		dir := filepath.Join(backupsBase, timestamp)
		if _, err := os.Stat(dir); err != nil {
			return "", fmt.Errorf("backup %q not found — run 'weft target backups <harness>' to list available backups", timestamp)
		}
		return dir, nil
	}

	entries, err := os.ReadDir(backupsBase)
	if os.IsNotExist(err) || len(entries) == 0 {
		return "", fmt.Errorf("no backups found — nothing to revert")
	}
	if err != nil {
		return "", fmt.Errorf("reading backups dir: %w", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 0 {
		return "", fmt.Errorf("no backups found — nothing to revert")
	}
	sort.Strings(dirs)
	return filepath.Join(backupsBase, dirs[len(dirs)-1]), nil
}

// countFiles returns the number of regular files under root.
func countFiles(root string) int {
	count := 0
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			count++
		}
		return nil
	})
	return count
}

func init() {
	rootCmd.AddCommand(targetCmd)
	targetCmd.AddCommand(targetListCmd, targetApplyCmd, targetBackupsCmd, targetRevertCmd)

	targetRevertCmd.Flags().StringVar(&revertBackup, "backup", "", "timestamp of a specific backup to restore (e.g. 20260605-143022)")
}
