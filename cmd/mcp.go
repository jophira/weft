package cmd

import (
	"context"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/config"
	weftmcp "github.com/jophira/weft/internal/mcp"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP server subcommands",
	// Override root PersistentPreRun so the MCP server skips update checks and auto-sync.
	PersistentPreRun: func(_ *cobra.Command, _ []string) {},
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start a Model Context Protocol server on stdio",
	Long: `Start a stdio-based MCP server exposing weft's profile, source, and
harness operations to any MCP-aware AI agent (Claude Code, Cursor, Codex, …).

Add to Claude Code's .claude/settings.json:

  {
    "mcpServers": {
      "weft": { "command": "weft", "args": ["mcp", "serve"] }
    }
  }`,
	RunE: func(_ *cobra.Command, _ []string) error {
		cfgDir, _ := config.DefaultDir()
		srv := weftmcp.NewServer(
			newRegistry(),
			newProfileManager(),
			weftmcp.Config{
				Version:         Version,
				ActiveProfileFn: func() string { return viper.GetString("active_profile") },
				ConfigDir:       cfgDir,
			},
		)
		return srv.Serve(context.Background(), os.Stdin, os.Stdout)
	},
}

func init() {
	mcpCmd.AddCommand(mcpServeCmd)
	rootCmd.AddCommand(mcpCmd)
}
