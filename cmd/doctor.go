package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

		// Report per-target health for the active profile, if any.
		active := activeProfileName()
		if active != "" {
			p, err := newProfileManager().Get(active)
			if err == nil {
				targets := p.ResolvedTargets()
				if len(targets) > 0 {
					fmt.Printf("\nActive profile %q — target health:\n", active)
					hReg := harness.NewRegistry(harness.Instances()...)
					for _, t := range targets {
						h, ok := hReg.Get(t)
						if !ok {
							fmt.Printf("  ✗ %s: unknown harness\n", t)
							continue
						}
						if h.Detect() {
							fmt.Printf("  ✓ %s: detected\n", t)
						} else {
							fmt.Printf("  – %s: not detected\n", t)
						}
					}
				}
			}
		} else {
			// No active profile — show a hint listing all known targets.
			profiles, _ := newProfileManager().List()
			if len(profiles) > 0 {
				var names []string
				for _, p := range profiles {
					names = append(names, p.Name)
				}
				fmt.Printf("\nNo active profile. Available: %s\n", strings.Join(names, ", "))
				fmt.Printf("Activate one with: weft profile use <name>\n")
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
