package cmd

import (
	"fmt"
	"io"
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
		runDoctor(os.Stdout)
		return nil
	},
}

// runDoctor writes the health check output to w. Shared by doctorCmd and bug-report.
func runDoctor(w io.Writer) {
	home, _ := os.UserHomeDir()

	fmt.Fprintln(w, "Jophira Health Check")
	fmt.Fprintln(w, "────────────────────")

	cfgDir := filepath.Join(home, ".config", "weft")
	if _, err := os.Stat(cfgDir); err == nil {
		fmt.Fprintf(w, "  ✓ Config dir: %s\n", cfgDir)
	} else {
		fmt.Fprintf(w, "  ✗ Config dir missing: %s\n", cfgDir)
	}

	fmt.Fprintln(w, "\nScanning for AI rule folders:")
	for _, k := range harness.All() {
		detected := k.H.Detect()
		displayPath := k.ConfigPath
		if cp, ok := k.H.(harness.ConfigPather); ok {
			displayPath = cp.ConfigPath()
		}
		if detected {
			fmt.Fprintf(w, "  ✓ Found: %s\n", displayPath)
		} else {
			fmt.Fprintf(w, "  – Not found: %s\n", displayPath)
		}
	}

	active := activeProfileName()
	if active != "" {
		p, err := newProfileManager().Get(active)
		if err == nil {
			targets := p.ResolvedTargets()
			if len(targets) > 0 {
				fmt.Fprintf(w, "\nActive profile %q — target health:\n", active)
				hReg := harness.NewRegistry(harness.Instances()...)
				for _, t := range targets {
					h, ok := hReg.Get(t)
					if !ok {
						fmt.Fprintf(w, "  ✗ %s: unknown harness\n", t)
						continue
					}
					if h.Detect() {
						fmt.Fprintf(w, "  ✓ %s: detected\n", t)
					} else {
						fmt.Fprintf(w, "  – %s: not detected\n", t)
					}
				}
			}
		}
	} else {
		profiles, _ := newProfileManager().List()
		if len(profiles) > 0 {
			var names []string
			for _, p := range profiles {
				names = append(names, p.Name)
			}
			fmt.Fprintf(w, "\nNo active profile. Available: %s\n", strings.Join(names, ", "))
			fmt.Fprintf(w, "Activate one with: weft profile use <name>\n")
		}
	}
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
