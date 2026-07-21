package harness

import (
	"path/filepath"
	"testing"

	"github.com/jophira/weft/internal/manifest"
)

// An upgraded install carries a manifest with no "staged" field. Once weft also
// tracks an MCP sidecar, the next apply must leave that ownership record alone.
//
// Before the fix, StagedSet's legacy fallback readmitted the sidecar key, and
// pruneDropped joined it onto the target root. On Unix the resulting path simply
// did not exist, so the entry was silently deleted and weft forgot it owned the
// file. On Windows the embedded drive colon makes the path invalid outright, so
// the read failed with something other than IsNotExist and aborted the apply.
//
// The key is built from the real target dir so the Windows case reproduces
// natively rather than needing a synthetic drive letter.
func TestApply_LegacyManifestKeepsMCPSidecar(t *testing.T) {
	f := newApplyFixture(t)
	f.apply(t, map[string]string{"CLAUDE.md": "v1"})

	sidecar := "mcp:" + filepath.ToSlash(filepath.Join(f.target, "..", ".claude.json"))
	m := f.manifest(t)
	m.Files[sidecar] = "sha256:deadbeef"
	m.Staged = nil // as written by a weft predating the Staged field
	if err := manifest.Save(f.ctx.CfgDir, m); err != nil {
		t.Fatalf("saving manifest: %v", err)
	}

	f.apply(t, map[string]string{"CLAUDE.md": "v2"})

	if _, ok := f.manifest(t).Files[sidecar]; !ok {
		t.Errorf("sidecar entry %q was pruned from the manifest", sidecar)
	}
}
