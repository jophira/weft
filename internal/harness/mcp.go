package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/mcpconfig"
)

// MCPFileName is the staged name of the canonical MCP document, exported for
// callers that stage or locate it.
const MCPFileName = mcpFileName

// ProjectMCP renders the canonical MCP config into one harness's native format.
//
// MCP is projected out-of-band rather than copied, for the same reason the
// instruction file is: the destination is a shared document owned by the tool
// (~/.claude.json carries project history, config.toml carries model settings),
// so weft merges into its own key instead of writing a file of its own.
//
// It is a no-op for harnesses with no MCP dialect, and when the profile's
// harness_sync config withholds the mcp class.
func ProjectMCP(h Harness, cfg mcpconfig.Config, ctx ApplyCtx) error {
	if !ctx.classAllowed(ClassMCP) {
		return nil
	}
	d, ok := mcpconfig.DialectFor(h.Name())
	if !ok {
		return nil // harness has no MCP support
	}

	path, err := d.Path()
	if err != nil {
		return fmt.Errorf("resolving %s mcp path: %w", h.Name(), err)
	}

	existing, err := os.ReadFile(path) //nolint:gosec // path is resolved from the dialect, not user input
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	// Refuse to touch a document weft cannot parse. Overwriting it would destroy
	// whatever the user has there, which is exactly the data loss the keyed merge
	// exists to prevent.
	rendered, err := d.ToNative(cfg, existing)
	if err != nil {
		return fmt.Errorf("rendering mcp config for %s: %w", h.Name(), err)
	}

	return writeTrackedSidecar(path, mcpManifestKey(path), h.Name(), rendered, ctx)
}

// mcpManifestKey is the manifest key for a harness's native MCP file.
//
// The native file usually sits outside the harness's target root (~/.claude.json
// is a sibling of ~/.claude), so it has no meaningful relative path. The absolute
// path is used instead, prefixed so it can never collide with a staged rel path
// and is obvious when reading a manifest by hand.
func mcpManifestKey(path string) string {
	return "mcp:" + filepath.ToSlash(path)
}

// writeTrackedSidecar writes a file weft owns a key inside, recording its hash in
// the manifest without disturbing the staged set.
//
// It deliberately does not touch Staged or TargetRoot, unlike trackAndWriteFile.
// Those fields describe the copied file tree; a sidecar that claimed membership
// would make pruneDropped delete it on the next apply, and would overwrite the
// target root of a multi-file harness.
func writeTrackedSidecar(absPath, key, harnessName string, content []byte, ctx ApplyCtx) error {
	out := applyOut(ctx)
	display := locate.Tilde(absPath)

	m, err := manifest.Load(ctx.CfgDir, harnessName)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	contentHash := manifest.HashBytes(content)
	existing, readErr := os.ReadFile(absPath) //nolint:gosec // path resolved from harness config

	switch {
	case errors.Is(readErr, os.ErrNotExist):
		// new file — fall through to write

	case readErr != nil:
		return fmt.Errorf("reading %s: %w", absPath, readErr)

	default:
		existingHash := manifest.HashBytes(existing)
		if knownHash, owned := m.Files[key]; owned && existingHash == knownHash {
			if contentHash == knownHash {
				fmt.Fprintf(out, logUnchanged, statusUnchanged, display)
				return nil
			}
		} else {
			// Either weft has never written this file, or someone changed it since.
			// The merge already preserved every key weft does not own, so the write
			// is safe — but the user should still get a copy of what was there.
			backupDir, bErr := backupConflicts([]conflictFile{{rel: filepath.Base(absPath), abs: absPath}}, harnessName, ctx.CfgDir)
			if bErr != nil {
				return bErr
			}
			fmt.Fprintf(out, "  ! %s changed outside weft — backed up to %s\n", display, locate.Tilde(backupDir))
		}
	}

	if mkErr := os.MkdirAll(filepath.Dir(absPath), 0o755); mkErr != nil {
		return fmt.Errorf("creating parent dir for %s: %w", absPath, mkErr)
	}
	if wErr := os.WriteFile(absPath, content, 0o600); wErr != nil { //nolint:gosec // path resolved from harness config
		return fmt.Errorf("writing %s: %w", absPath, wErr)
	}
	fmt.Fprintf(out, logWrote, statusWrote, display)

	m.Harness = harnessName
	if ctx.ProfileName != "" {
		m.Profile = ctx.ProfileName
	}
	m.Files[key] = contentHash
	return manifest.Save(ctx.CfgDir, m)
}
