package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jophira/weft/internal/profile"
)

type profileSummary struct {
	Name     string   `json:"name"`
	Sources  []string `json:"sources"`
	Overlay  string   `json:"overlay"`
	Targets  []string `json:"targets"`
	IsActive bool     `json:"is_active"`
}

func profileListTool() mcplib.Tool {
	return mcplib.NewTool("weft_profile_list",
		mcplib.WithDescription("List all weft profiles with their sources, targets, and active status."),
		mcplib.WithReadOnlyHintAnnotation(true),
	)
}

func profileListHandler(pm *profile.FileManager, activeFn func() string) server.ToolHandlerFunc {
	return func(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		profiles, err := pm.List()
		if err != nil {
			return nil, fmt.Errorf("listing profiles: %w", err)
		}
		active := activeFn()
		summaries := make([]profileSummary, 0, len(profiles))
		for _, p := range profiles {
			summaries = append(summaries, profileSummary{
				Name:     p.Name,
				Sources:  p.Sources,
				Overlay:  string(p.Overlay),
				Targets:  p.ResolvedTargets(),
				IsActive: p.Name == active,
			})
		}
		out, err := json.MarshalIndent(summaries, "", "  ")
		if err != nil {
			return nil, err
		}
		return mcplib.NewToolResultText(string(out)), nil
	}
}

type profileDetail struct {
	profileSummary
	WriteBackDefault   string            `json:"write_back_default,omitempty"`
	WriteBackOverrides map[string]string `json:"write_back_overrides,omitempty"`
}

func profileInspectTool() mcplib.Tool {
	return mcplib.NewTool("weft_profile_inspect",
		mcplib.WithDescription("Inspect a named profile, returning its full configuration and whether it is currently active."),
		mcplib.WithString("name", mcplib.Required(), mcplib.Description("Profile name to inspect")),
		mcplib.WithReadOnlyHintAnnotation(true),
	)
}

func profileInspectHandler(pm *profile.FileManager, activeFn func() string) server.ToolHandlerFunc {
	return func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		name := mcplib.ParseString(req, "name", "")
		if name == "" {
			return mcplib.NewToolResultError("name is required"), nil
		}
		p, err := pm.Get(name)
		if err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}
		detail := profileDetail{
			profileSummary: profileSummary{
				Name:     p.Name,
				Sources:  p.Sources,
				Overlay:  string(p.Overlay),
				Targets:  p.ResolvedTargets(),
				IsActive: p.Name == activeFn(),
			},
			WriteBackDefault:   p.WriteBack.Default,
			WriteBackOverrides: p.WriteBack.Overrides,
		}
		out, err := json.MarshalIndent(detail, "", "  ")
		if err != nil {
			return nil, err
		}
		return mcplib.NewToolResultText(string(out)), nil
	}
}
