package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/harness"
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
		// Resolve active profile.
		activeName := viper.GetString("active_profile")
		if activeName == "" {
			return fmt.Errorf("no active profile — run 'weft profile use <name>' first")
		}

		// Locate the staged directory produced by the last profile use.
		cfgDir, err := config.DefaultDir()
		if err != nil {
			return err
		}
		stagedDir := filepath.Join(cfgDir, "staged", activeName)
		if _, err := os.Stat(stagedDir); err != nil {
			return fmt.Errorf(
				"staged output for profile %q not found\n"+
					"  run 'weft profile use %s' to merge and stage first",
				activeName, activeName,
			)
		}

		// Look up the harness.
		reg := harness.NewRegistry(harness.Instances()...)
		h, ok := reg.Get(args[0])
		if !ok {
			return fmt.Errorf(
				"unknown harness %q\n  run 'weft target list' to see supported harnesses",
				args[0],
			)
		}

		fmt.Printf("Applying profile %q → %s...\n", activeName, args[0])
		if err := h.Apply(stagedDir); err != nil {
			return fmt.Errorf("apply failed: %w", err)
		}
		fmt.Printf("✓ Applied\n")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(targetCmd)
	targetCmd.AddCommand(targetListCmd, targetApplyCmd)
}
