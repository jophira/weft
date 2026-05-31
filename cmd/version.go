package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var Version = "0.1.0-dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("weft %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
