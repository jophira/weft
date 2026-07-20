package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/mcpconfig"
)

// stageMCPConfig reads the merged canonical MCP document out of the staged tree
// and removes it, returning the parsed config.
//
// The staged tree is the already-merged result of every source in the profile,
// so source precedence has been resolved by the time this runs — this only has
// to parse one file. Removing it mirrors how CLAUDE.md is handled: the document
// is projected per harness dialect, never copied verbatim into a target.
//
// A missing file is not an error; it simply means no source defines MCP servers.
func stageMCPConfig(stagedDir string) (mcpconfig.Config, error) {
	path := filepath.Join(stagedDir, harness.MCPFileName)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return mcpconfig.Config{}, nil
	} else if err != nil {
		return mcpconfig.Config{}, fmt.Errorf("checking for %s: %w", harness.MCPFileName, err)
	}

	cfg, err := mcpconfig.Load(path)
	if err != nil {
		// Load already validates, so this covers both malformed YAML and a
		// literal credential — both of which must stop the apply rather than
		// silently project a broken or unsafe MCP config.
		return mcpconfig.Config{}, fmt.Errorf("loading staged mcp config: %w", err)
	}

	if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
		return mcpconfig.Config{}, fmt.Errorf("removing merged mcp config from staging: %w", rmErr)
	}
	return cfg, nil
}
