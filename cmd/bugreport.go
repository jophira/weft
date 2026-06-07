package cmd

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/jophira/weft/internal/logger"
	"github.com/spf13/cobra"
)

var bugReportCmd = &cobra.Command{
	Use:   "bug-report",
	Short: "Print diagnostic info for filing a bug report",
	Long: `Collect version, environment, doctor output, and recent log entries
into a block suitable for pasting into a GitHub issue.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		w := os.Stdout

		fmt.Fprintln(w, "=== weft bug report ===")
		fmt.Fprintf(w, "Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
		fmt.Fprintln(w)

		fmt.Fprintln(w, "-- Version --")
		fmt.Fprintf(w, "weft %s (commit %s, built %s)\n", Version, Commit, Date)
		fmt.Fprintln(w)

		fmt.Fprintln(w, "-- Environment --")
		fmt.Fprintf(w, "OS: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		fmt.Fprintln(w)

		fmt.Fprintln(w, "-- Config --")
		logPath := logger.LogPath()
		if logPath == "" {
			fmt.Fprintln(w, "Log file: (not initialised)")
		} else {
			fmt.Fprintf(w, "Log file: %s\n", logPath)
		}
		fmt.Fprintln(w)

		fmt.Fprintln(w, "-- Doctor --")
		runDoctor(w)
		fmt.Fprintln(w)

		fmt.Fprintln(w, "-- Log (last 50 lines) --")
		tail := logger.Tail(50)
		if len(tail) == 0 {
			fmt.Fprintln(w, "(no log entries yet)")
		} else {
			_, _ = w.Write(tail)
		}
		fmt.Fprintln(w)

		fmt.Fprintln(w, "=== paste everything above into your GitHub issue ===")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(bugReportCmd)
}
