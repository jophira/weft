package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jophira/weft/internal/harness"
	"github.com/spf13/cobra"
)

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
		for _, k := range harness.All() {
			detected := k.H.Detect()
			displayPath := k.ConfigPath
			if cp, ok := k.H.(harness.ConfigPather); ok {
				displayPath = cp.ConfigPath()
			}
			if detected {
				fmt.Printf("  ✓ Found: %s\n", displayPath)
			} else {
				fmt.Printf("  – Not found: %s\n", displayPath)
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
