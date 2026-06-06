package mcp

import (
	"context"
	"encoding/json"
	"os"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/profile"
)

type harnessHealth struct {
	Name     string `json:"name"`
	Detected bool   `json:"detected"`
}

type doctorResult struct {
	ConfigOk      bool            `json:"config_ok"`
	ConfigDir     string          `json:"config_dir"`
	Harnesses     []harnessHealth `json:"harnesses"`
	ActiveProfile string          `json:"active_profile,omitempty"`
	TargetHealth  []harnessHealth `json:"target_health,omitempty"`
}

func doctorTool() mcplib.Tool {
	return mcplib.NewTool("weft_doctor",
		mcplib.WithDescription("Run a weft health check: verify the config directory, detect installed harnesses, and report target health for the active profile."),
		mcplib.WithReadOnlyHintAnnotation(true),
	)
}

func doctorHandler(activeFn func() string, pm *profile.FileManager) server.ToolHandlerFunc {
	return func(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		cfgDir, _ := config.DefaultDir()
		result := doctorResult{ConfigDir: cfgDir}
		_, err := os.Stat(cfgDir)
		result.ConfigOk = err == nil

		for _, k := range harness.All() {
			result.Harnesses = append(result.Harnesses, harnessHealth{
				Name:     k.H.Name(),
				Detected: k.H.Detect(),
			})
		}

		activeName := activeFn()
		result.ActiveProfile = activeName
		if activeName != "" {
			if p, err := pm.Get(activeName); err == nil {
				hReg := harness.NewRegistry(harness.Instances()...)
				for _, t := range p.ResolvedTargets() {
					h, ok := hReg.Get(t)
					result.TargetHealth = append(result.TargetHealth, harnessHealth{
						Name:     t,
						Detected: ok && h.Detect(),
					})
				}
			}
		}

		out, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return nil, err
		}
		return mcplib.NewToolResultText(string(out)), nil
	}
}
