package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jophira/weft/internal/collect"
	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/merge"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

func activeProfileResource(pm *profile.FileManager, reg *source.FileRegistry, activeFn func() string) server.ResourceHandlerFunc {
	return func(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
		activeName := activeFn()
		if activeName == "" {
			return textResource(req.Params.URI, "(no active profile)"), nil
		}
		p, err := pm.Get(activeName)
		if err != nil {
			return nil, err
		}
		text, err := mergeProfileInstructions(p, reg)
		if err != nil {
			return nil, err
		}
		return textResource(req.Params.URI, text), nil
	}
}

func sourceInstructionsResource(reg *source.FileRegistry) server.ResourceTemplateHandlerFunc {
	return func(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
		// URI form: weft://source/{name}/instructions
		name := uriParam(req.Params.URI, "source", "instructions")
		if name == "" {
			return nil, fmt.Errorf("cannot parse source name from URI: %s", req.Params.URI)
		}
		s, err := reg.Get(name)
		if err != nil {
			return nil, err
		}
		data, err := collect.Collect(locate.ExpandHome(s.Root), s.Structure.InstructionGlob, s.Structure.ManagedDirs()...)
		if err != nil {
			return nil, err
		}
		return textResource(req.Params.URI, string(data)), nil
	}
}

func harnessCurrentResource(cfgDir string) server.ResourceTemplateHandlerFunc {
	return func(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
		// URI form: weft://harness/{name}/current
		name := uriParam(req.Params.URI, "harness", "current")
		if name == "" {
			return nil, fmt.Errorf("cannot parse harness name from URI: %s", req.Params.URI)
		}
		dir := cfgDir
		if dir == "" {
			var err error
			dir, err = config.DefaultDir()
			if err != nil {
				return nil, err
			}
		}
		m, err := manifest.Load(dir, name)
		if err != nil {
			return nil, fmt.Errorf("loading manifest for %s: %w", name, err)
		}
		if m.TargetRoot == "" {
			return nil, fmt.Errorf("no weft manifest found for harness %q — has it been applied?", name)
		}
		text, err := readRootFiles(m.TargetRoot, m.Files)
		if err != nil {
			return nil, err
		}
		return textResource(req.Params.URI, text), nil
	}
}

// mergeProfileInstructions builds the merged instruction text for a profile in memory.
func mergeProfileInstructions(p *profile.Profile, reg *source.FileRegistry) (string, error) {
	strategy := merge.ForOverlay(p.Overlay)
	var merged []byte
	for _, srcName := range p.Sources {
		s, err := reg.Get(srcName)
		if err != nil {
			return "", err
		}
		data, err := collect.Collect(locate.ExpandHome(s.Root), s.Structure.InstructionGlob, s.Structure.ManagedDirs()...)
		if err != nil {
			return "", fmt.Errorf("collecting from source %s: %w", srcName, err)
		}
		merged, err = strategy(merged, data)
		if err != nil {
			return "", err
		}
	}
	return string(merged), nil
}

// readRootFiles reads the root-level weft-managed files from targetRoot
// (skipping anything in a subdirectory). Files are read in sorted key order
// so the output is deterministic across successive calls.
func readRootFiles(targetRoot string, files map[string]string) (string, error) {
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	var buf []byte
	for _, rel := range rels {
		if strings.ContainsRune(rel, '/') || strings.ContainsRune(rel, filepath.Separator) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(targetRoot, rel))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if len(buf) > 0 && buf[len(buf)-1] != '\n' {
			buf = append(buf, '\n')
		}
		buf = append(buf, data...)
	}
	return string(buf), nil
}

// uriParam extracts the middle segment from URIs of the form weft://{prefix}/{name}/{suffix}.
func uriParam(uri, prefix, suffix string) string {
	trimmed := strings.TrimPrefix(uri, "weft://")
	parts := strings.SplitN(trimmed, "/", 3)
	if len(parts) != 3 || parts[0] != prefix || parts[2] != suffix {
		return ""
	}
	return parts[1]
}

func textResource(uri, text string) []mcplib.ResourceContents {
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{URI: uri, MIMEType: "text/plain", Text: text},
	}
}
