package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// knownRuleDirs are common AI harness config locations to scan.
var knownRuleDirs = []string{
	"~/.claude",
	"~/.cursor",
	"~/.warp",
	"~/.config/aider",
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check system health and discover AI rule folders",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := os.UserHomeDir()

		fmt.Println("Jophira Health Check")
		fmt.Println("────────────────────")

		cfgDir := filepath.Join(home, ".config", "weft")
		if _, err := os.Stat(cfgDir); err == nil {
			fmt.Printf("  ✓ Config dir: %s\n", cfgDir)
		} else {
			fmt.Printf("  ✗ Config dir missing: %s\n", cfgDir)
		}

		fmt.Println("\nScanning for AI rule folders:")
		for _, d := range knownRuleDirs {
			expanded := filepath.Join(home, d[1:])
			if _, err := os.Stat(expanded); err == nil {
				fmt.Printf("  ✓ Found: %s\n", d)
			} else {
				fmt.Printf("  – Not found: %s\n", d)
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
