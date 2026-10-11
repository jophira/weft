package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
)

// settingsFileName is the file a source supplies and a harness reads.
const settingsFileName = "settings.json"

// SettingsOwner is an optional Harness extension for tools whose settings.json
// weft projects key by key (currently Claude Code).
type SettingsOwner interface {
	// SettingsPath is the absolute path of the live settings file.
	SettingsPath() (string, error)
}

// SettingsSource is one source's settings document, already anchor-expanded.
type SettingsSource struct {
	Name string // source name, recorded as the owner of each key
	Path string // where the bytes came from, for error messages
	Data []byte
}

// SettingsSourcePath is where a source root keeps a harness's settings file.
func SettingsSourcePath(root, harnessName string) string {
	return filepath.Join(root, "harness", harnessName, settingsFileName)
}

// settingsManifestKey is the sidecar Files key for a live settings file.
func settingsManifestKey(path string) string {
	return "settings:" + filepath.ToSlash(path)
}

// ProjectSettings merges the sources' top-level keys into the harness's live
// settings file. Later sources win per key. Keys weft wrote on an earlier apply
// and no source names any more are removed; every other live key is untouched.
//
// It is a no-op when the class is withheld, the harness has no settings file to
// manage, or no source supplies settings and weft never owned a key there. In
// that last case the live file is not even read, so users without source
// settings never get their file reformatted.
func ProjectSettings(h Harness, srcs []SettingsSource, ctx ApplyCtx) error {
	owner, ok := h.(SettingsOwner)
	if !ok || !ctx.classAllowed(ClassSettings) {
		return nil
	}
	path, err := owner.SettingsPath()
	if err != nil {
		return fmt.Errorf("resolving %s settings path: %w", h.Name(), err)
	}

	want := map[string]json.RawMessage{}
	owners := map[string]string{}
	for _, s := range srcs {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(s.Data, &doc); err != nil || doc == nil {
			return fmt.Errorf("%s is not a JSON object: %v", s.Path, err)
		}
		for k, v := range doc {
			want[k] = v
			owners[k] = s.Name
		}
	}

	m, err := manifest.Load(ctx.CfgDir, h.Name())
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}
	prev := m.Settings[path]
	if len(want) == 0 && len(prev) == 0 {
		return nil
	}

	live, err := os.ReadFile(path) //nolint:gosec // path resolved from the harness, not user input
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	cur := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(live)) > 0 {
		if err := json.Unmarshal(live, &cur); err != nil || cur == nil {
			return fmt.Errorf("%s is not a JSON object, refusing to overwrite it: %v", path, err)
		}
	}

	next := make(map[string]json.RawMessage, len(cur)+len(want))
	for k, v := range cur {
		next[k] = v
	}
	for k := range prev {
		if _, stillWanted := want[k]; !stillWanted {
			delete(next, k)
		}
	}
	for k, v := range want {
		next[k] = v
	}

	key := settingsManifestKey(path)
	if sameSettings(cur, next) {
		fmt.Fprintf(applyOut(ctx), logUnchanged, statusUnchanged, locate.Tilde(path))
	} else {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(next); err != nil {
			return fmt.Errorf("encoding %s: %w", path, err)
		}
		if err := writeTrackedSidecar(path, key, h.Name(), buf.Bytes(), ctx); err != nil {
			return err
		}
	}

	// writeTrackedSidecar saved the manifest itself; reload before recording
	// ownership or this save clobbers its hash.
	if m, err = manifest.Load(ctx.CfgDir, h.Name()); err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}
	if len(live) > 0 && sameSettings(cur, next) {
		m.Files[key] = manifest.HashBytes(live)
	}
	if len(want) == 0 {
		// Owning nothing means the file is the user's again: forget its hash too,
		// or adopt would refuse it as "already managed" for good.
		delete(m.Settings, path)
		delete(m.Files, key)
	} else {
		m.Settings[path] = owners
	}
	return manifest.Save(ctx.CfgDir, m)
}

// sameSettings compares two settings documents by meaning: same keys, and per
// key the same JSON once whitespace is removed. Claude Code writes its file in
// insertion order, so a byte comparison would rewrite it on every apply.
func sameSettings(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || compactJSON(av) != compactJSON(bv) {
			return false
		}
	}
	return true
}

func compactJSON(v json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, v); err != nil {
		return string(v)
	}
	return buf.String()
}
