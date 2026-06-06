package mcp

import (
	"context"
	"io"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

// Config holds caller-supplied dependencies that vary by context (viper bindings, version).
type Config struct {
	Version         string
	ActiveProfileFn func() string // reads active_profile from viper or equivalent
	ConfigDir       string        // ~/.config/weft — used for manifest lookup
}

// Server wraps an mcp-go MCPServer wired to weft's internal packages.
type Server struct {
	mcp *server.MCPServer
}

// NewServer creates and wires the MCP server with all tools and resources.
func NewServer(reg *source.FileRegistry, pm *profile.FileManager, cfg Config) *Server {
	s := server.NewMCPServer(
		"weft",
		cfg.Version,
		server.WithDescription("Composable AI rules manager — inspect and control weft rule sources, profiles, and harness targets."),
	)

	// Profile tools (read-only)
	s.AddTool(profileListTool(), profileListHandler(pm, cfg.ActiveProfileFn))
	s.AddTool(profileInspectTool(), profileInspectHandler(pm, cfg.ActiveProfileFn))

	// Source tools
	s.AddTool(sourceListTool(), sourceListHandler(reg))
	s.AddTool(sourceStatusTool(), sourceStatusHandler(reg))
	s.AddTool(sourceSyncTool(), sourceSyncHandler(reg))
	s.AddTool(sourcePushTool(), sourcePushHandler(reg))

	// Doctor
	s.AddTool(doctorTool(), doctorHandler(cfg.ActiveProfileFn, pm))

	// Resources
	s.AddResource(
		mcplib.NewResource(
			"weft://profile/active",
			"Active profile instructions",
			mcplib.WithMIMEType("text/plain"),
			mcplib.WithResourceDescription("Merged instruction text produced by the currently active profile"),
		),
		activeProfileResource(pm, reg, cfg.ActiveProfileFn),
	)
	s.AddResourceTemplate(
		mcplib.NewResourceTemplate(
			"weft://source/{name}/instructions",
			"Source instructions",
			mcplib.WithTemplateMIMEType("text/plain"),
			mcplib.WithTemplateDescription("Raw instruction content assembled from a named source"),
		),
		sourceInstructionsResource(reg),
	)
	s.AddResourceTemplate(
		mcplib.NewResourceTemplate(
			"weft://harness/{name}/current",
			"Harness current content",
			mcplib.WithTemplateMIMEType("text/plain"),
			mcplib.WithTemplateDescription("Instruction content currently written to a harness on disk"),
		),
		harnessCurrentResource(cfg.ConfigDir),
	)

	return &Server{mcp: s}
}

// Serve runs the MCP server over the given reader/writer until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	stdio := server.NewStdioServer(s.mcp)
	return stdio.Listen(ctx, r, w)
}
