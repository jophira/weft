package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jophira/weft/internal/autosync"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/update"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "weft",
	Short: "Composable AI rules manager",
	Long:  "Weft by Jophira — manage, layer, and sync AI rule sources across teams and harnesses.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if cmd.Name() == "update" {
			return
		}

		// Update prompt — interactive TTY only.
		if isInteractiveTTY() {
			if result, err := update.Check(Version); err == nil && result.Newer {
				fmt.Fprintf(os.Stderr, "\nA new release of weft is available: v%s → v%s\n", result.Current, result.Latest)
				fmt.Fprint(os.Stderr, "Update now? [Y/n/ignore] ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				switch strings.TrimSpace(strings.ToLower(line)) {
				case "", "y", "yes":
					fmt.Fprintf(os.Stderr, "Updating weft v%s → v%s\n", result.Current, result.Latest)
					if err := doUpdate(result.Latest); err != nil {
						fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
					} else {
						os.Exit(0)
					}
				case "ignore", "i":
					if err := update.IgnoreVersion(result.Latest); err != nil {
						fmt.Fprintf(os.Stderr, "Could not save preference: %v\n", err)
					}
					fmt.Fprintf(os.Stderr, "Ignoring v%s — you'll be notified when a newer version ships.\n", result.Latest)
				}
				// "n" or anything else: fall through
			}
		}

		// Auto-sync — runs on every invocation except CI.
		runAutoSync()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: $HOME/.config/weft/config.yaml)")
}

// isInteractiveTTY returns false when stdin is a pipe, redirect, or CI environment,
// preventing the update prompt from blocking non-interactive usage.
func isInteractiveTTY() bool {
	if os.Getenv("CI") != "" {
		return false
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func runAutoSync() {
	if os.Getenv("CI") != "" {
		return
	}
	sources, err := newRegistry().List()
	if err != nil || len(sources) == 0 {
		return
	}
	stateFile, err := autosync.DefaultStateFilePath()
	if err != nil {
		return
	}
	syncFn := func(s source.Source) (bool, error) {
		return runSync(s, io.Discard)
	}
	_ = autosync.Run(sources, stateFile, autosync.DefaultInterval, syncFn, os.Stderr)
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		viper.AddConfigPath(fmt.Sprintf("%s/.config/weft", home))
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}
	viper.AutomaticEnv()
	_ = viper.ReadInConfig()
}
