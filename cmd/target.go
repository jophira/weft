package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var targetCmd = &cobra.Command{
	Use:   "target",
	Short: "Manage AI harness targets",
}

var targetApplyCmd = &cobra.Command{
	Use:   "apply <harness>",
	Short: "Apply the active profile to a harness (claude-code, cursor, warp)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Applying active profile to harness: %s\n", args[0])
		return nil
	},
}

var targetListCmd = &cobra.Command{
	Use:   "list",
	Short: "Detect and list AI harnesses installed on this machine",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Detecting installed harnesses...")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(targetCmd)
	targetCmd.AddCommand(targetApplyCmd, targetListCmd)
}
