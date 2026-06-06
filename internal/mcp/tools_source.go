package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jophira/weft/internal/git"
	"github.com/jophira/weft/internal/source"
)

type sourceSummary struct {
	Name   string `json:"name"`
	Root   string `json:"root"`
	Remote string `json:"remote"`
	Branch string `json:"branch"`
	Dirty  bool   `json:"dirty"`
	Error  string `json:"error,omitempty"`
}

func sourceListTool() mcplib.Tool {
	return mcplib.NewTool("weft_source_list",
		mcplib.WithDescription("List all registered weft rule sources with their basic git state."),
		mcplib.WithReadOnlyHintAnnotation(true),
	)
}

func sourceListHandler(reg *source.FileRegistry) server.ToolHandlerFunc {
	return func(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		sources, err := reg.List()
		if err != nil {
			return nil, fmt.Errorf("listing sources: %w", err)
		}
		summaries := make([]sourceSummary, 0, len(sources))
		for _, s := range sources {
			sum := sourceSummary{
				Name:   s.Name,
				Root:   s.Root,
				Remote: s.Remote,
				Branch: s.Branch,
			}
			expanded := source.ExpandHome(s.Root)
			if git.IsRepo(expanded) {
				if repo, err := git.Open(expanded); err == nil {
					if clean, err := repo.IsClean(); err == nil {
						sum.Dirty = !clean
					}
				}
			}
			summaries = append(summaries, sum)
		}
		out, err := json.MarshalIndent(summaries, "", "  ")
		if err != nil {
			return nil, err
		}
		return mcplib.NewToolResultText(string(out)), nil
	}
}

type sourceStatusDetail struct {
	sourceSummary
	Status          string `json:"status"`
	InstructionGlob string `json:"instruction_glob"`
}

func sourceStatusTool() mcplib.Tool {
	return mcplib.NewTool("weft_source_status",
		mcplib.WithDescription("Show detailed status of a single source, including the full git working-tree diff summary."),
		mcplib.WithString("name", mcplib.Required(), mcplib.Description("Source name")),
		mcplib.WithReadOnlyHintAnnotation(true),
	)
}

func sourceStatusHandler(reg *source.FileRegistry) server.ToolHandlerFunc {
	return func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		name := mcplib.ParseString(req, "name", "")
		if name == "" {
			return mcplib.NewToolResultError("name is required"), nil
		}
		s, err := reg.Get(name)
		if err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}
		detail := sourceStatusDetail{
			sourceSummary: sourceSummary{
				Name:   s.Name,
				Root:   s.Root,
				Remote: s.Remote,
				Branch: s.Branch,
			},
			InstructionGlob: s.Structure.InstructionGlob,
		}
		expanded := source.ExpandHome(s.Root)
		if git.IsRepo(expanded) {
			if repo, err := git.Open(expanded); err == nil {
				if statusStr, err := repo.Status(); err == nil {
					detail.Status = statusStr
					detail.Dirty = statusStr != ""
				}
			}
		}
		out, err := json.MarshalIndent(detail, "", "  ")
		if err != nil {
			return nil, err
		}
		return mcplib.NewToolResultText(string(out)), nil
	}
}

type syncResult struct {
	Name    string `json:"name"`
	Updated bool   `json:"updated"`
	Error   string `json:"error,omitempty"`
}

func sourceSyncTool() mcplib.Tool {
	return mcplib.NewTool("weft_source_sync",
		mcplib.WithDescription("Pull latest commits from remote for one named source, or all sources when name is omitted."),
		mcplib.WithString("name", mcplib.Description("Source name; omit to sync all sources")),
	)
}

func sourceSyncHandler(reg *source.FileRegistry) server.ToolHandlerFunc {
	return func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		name := mcplib.ParseString(req, "name", "")
		var sources []source.Source
		if name != "" {
			s, err := reg.Get(name)
			if err != nil {
				return mcplib.NewToolResultError(err.Error()), nil
			}
			sources = []source.Source{*s}
		} else {
			var err error
			sources, err = reg.List()
			if err != nil {
				return nil, fmt.Errorf("listing sources: %w", err)
			}
		}
		results := make([]syncResult, 0, len(sources))
		for _, s := range sources {
			r := syncResult{Name: s.Name}
			updated, err := syncSource(s)
			if err != nil {
				r.Error = err.Error()
			} else {
				r.Updated = updated
			}
			results = append(results, r)
		}
		out, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return nil, err
		}
		return mcplib.NewToolResultText(string(out)), nil
	}
}

// syncSource clones (if missing) or pulls a single source.
// Returns true when new commits were fetched.
func syncSource(s source.Source) (bool, error) {
	expanded := source.ExpandHome(s.Root)
	auth, err := git.ResolveAuth(s.Remote)
	if err != nil {
		return false, fmt.Errorf("resolving auth: %w", err)
	}
	if _, err := os.Stat(expanded); os.IsNotExist(err) {
		if err := git.Clone(s.Remote, expanded, s.Branch, auth, io.Discard); err != nil {
			return false, err
		}
		return true, nil
	}
	if !git.IsRepo(expanded) {
		return false, fmt.Errorf("%s exists but is not a git repository", s.Root)
	}
	repo, err := git.Open(expanded)
	if err != nil {
		return false, err
	}
	return repo.Pull(s.Branch, auth)
}

type pushResult struct {
	Name      string `json:"name"`
	Committed bool   `json:"committed"`
	Pushed    bool   `json:"pushed"`
}

func sourcePushTool() mcplib.Tool {
	return mcplib.NewTool("weft_source_push",
		mcplib.WithDescription("Stage all changes in a source, commit with the provided message, and push to the remote."),
		mcplib.WithString("name", mcplib.Required(), mcplib.Description("Source name")),
		mcplib.WithString("message", mcplib.Required(), mcplib.Description("Commit message describing what changed in the rules")),
	)
}

func sourcePushHandler(reg *source.FileRegistry) server.ToolHandlerFunc {
	return func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		name := mcplib.ParseString(req, "name", "")
		message := mcplib.ParseString(req, "message", "")
		if name == "" {
			return mcplib.NewToolResultError("name is required"), nil
		}
		if message == "" {
			return mcplib.NewToolResultError("message is required — describe what changed in the rules"), nil
		}
		s, err := reg.Get(name)
		if err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}
		expanded := source.ExpandHome(s.Root)
		repo, err := git.Open(expanded)
		if err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}
		auth, err := git.ResolveAuth(s.Remote)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("resolving auth: %v", err)), nil
		}
		if err := repo.CommitAll(message); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("commit: %v", err)), nil
		}
		if err := repo.Push(auth); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("push: %v", err)), nil
		}
		out, _ := json.MarshalIndent(pushResult{Name: name, Committed: true, Pushed: true}, "", "  ")
		return mcplib.NewToolResultText(string(out)), nil
	}
}
