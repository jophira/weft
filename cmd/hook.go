package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Manage lifecycle hooks",
}

var hookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered hooks",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Hooks: (none registered yet)")
		return nil
	},
}

var hookRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Manually trigger a hook",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Running hook: %s\n", args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(hookCmd)
	hookCmd.AddCommand(hookListCmd, hookRunCmd)
}
