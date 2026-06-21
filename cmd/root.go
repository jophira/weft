package cmd

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/jophira/weft/internal/autosync"
	"github.com/jophira/weft/internal/logger"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/update"
	"github.com/jophira/weft/internal/validate"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// updateResultCh carries the async update-check outcome from PersistentPreRun to
// PersistentPostRun. Buffered with capacity 1 so the goroutine never blocks even
// if PostRun is skipped (e.g. the command exited early).
// cf. Java: a Future<Result> — the channel IS the future here.
var updateResultCh chan updateCheckResult

// updateCheckResult bundles the two values returned by update.Check.
type updateCheckResult struct {
	result update.Result
	err    error
}

var rootCmd = &cobra.Command{
	Use:   "weft",
	Short: "Composable AI rules manager",
	Long:  "Weft by Jophira — manage, layer, and sync AI rule sources across teams and harnesses.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		logger.Init(Version)
		slog.Info("run", slog.String("cmd", cmd.CommandPath()))

		if cmd.Name() == "update" {
			return
		}

		// Kick off the update check in a goroutine so it runs concurrently with
		// the main command — no TTY stall. The result is collected in PersistentPostRun.
		// cf. Java: a fire-and-forget CompletableFuture.supplyAsync()
		if isInteractiveTTY() {
			updateResultCh = make(chan updateCheckResult, 1) // buffered: goroutine never blocks
			go func() {
				r, err := update.Check(Version)
				updateResultCh <- updateCheckResult{result: r, err: err}
			}()
		}

		// Auto-sync — skip read-only/informational commands.
		if !isReadOnlyCmd(cmd) {
			runAutoSync()
		}
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if cmd.Name() == "update" || updateResultCh == nil {
			return
		}

		// Non-blocking receive: if the check finished in time, show the prompt;
		// otherwise skip silently — the user sees no delay either way.
		// cf. Java: future.isDone() ? future.get() : skip
		select {
		case r := <-updateResultCh:
			if r.err == nil && r.result.Newer {
				fmt.Fprintf(os.Stderr, "\nA new release of weft is available: v%s → v%s\n", r.result.Current, r.result.Latest)
				fmt.Fprint(os.Stderr, "Update now? [Y/n/ignore] ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				switch strings.TrimSpace(strings.ToLower(line)) {
				case "", "y", "yes":
					fmt.Fprintf(os.Stderr, "Updating weft v%s → v%s\n", r.result.Current, r.result.Latest)
					if err := doUpdate(r.result.Latest); err != nil {
						fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
					} else {
						os.Exit(0)
					}
				case "ignore", "i":
					if err := update.IgnoreVersion(r.result.Latest); err != nil {
						fmt.Fprintf(os.Stderr, "Could not save preference: %v\n", err)
					}
					fmt.Fprintf(os.Stderr, "Ignoring v%s — you'll be notified when a newer version ships.\n", r.result.Latest)
				}
				// "n" or anything else: fall through
			}
		default:
			// Update check still running — skip this invocation, no user-visible delay.
		}
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
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

// skipAutoSyncCmds are leaf command names that should not trigger background
// auto-sync — either because they are read-only (no point pulling) or because
// they already perform their own network operation (sync, push) and a
// concurrent auto-sync would just double the noise on failure.
var skipAutoSyncCmds = map[string]bool{
	"list":    true,
	"status":  true,
	"backups": true,
	"version": true,
	"help":    true,
	"diff":    true,
	"sync":    true,
	"push":    true,
}

// isReadOnlyCmd reports whether cmd should skip background auto-sync.
func isReadOnlyCmd(cmd *cobra.Command) bool {
	return skipAutoSyncCmds[cmd.Name()]
}

func runAutoSync() {
	if os.Getenv("CI") != "" {
		return
	}
	reg, err := newRegistry()
	if err != nil {
		return
	}
	sources, err := reg.List()
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
	viper.SetDefault("warn_instruction_size_kb", validate.DefaultWarnSizeKB)
	viper.AutomaticEnv()
	_ = viper.ReadInConfig()
}
