package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jophira/weft/internal/instruction"
)

// HookCommand is what weft wires into a project's session-start hook. It takes
// no arguments because the resolver already inspects the working directory,
// which is the whole reason the hook is the cheapest delivery available.
const HookCommand = "weft rules resolve"

// Delivered reports what one harness received.
type Delivered struct {
	Harness string
	// How names the mechanism used: hook, import or inline.
	How string
	// Path is the file weft wrote, absolute.
	Path string
	// Wrote is false when the file was already correct.
	Wrote bool
	// TrackedByGit is true when the written file is one git normally tracks, so
	// callers can warn about a diff the user will see.
	TrackedByGit bool
}

// DeliverHook adds a session-start hook running the resolver to a project
// settings file, returning whether it changed anything.
//
// The settings file is expected to be one git ignores (Claude Code's
// settings.local.json is the conventionally gitignored half of the pair), which
// is what makes this the only delivery costing no tracked diff at all.
//
// The file is merged, never replaced: it belongs to the user and may hold
// permissions, environment and other hooks. Only weft's own entry is added, and
// only when an equivalent one is absent.
func DeliverHook(root, relPath string) (bool, error) {
	path := filepath.Join(root, filepath.FromSlash(relPath))

	doc := map[string]any{}
	data, err := os.ReadFile(path) //nolint:gosec // path is inside the repository
	switch {
	case err == nil:
		if strings.TrimSpace(string(data)) != "" {
			if jsonErr := json.Unmarshal(data, &doc); jsonErr != nil {
				// Refuse rather than overwrite. The file is the user's, and a
				// parse failure here is far more likely to be their edit in
				// progress than a weft bug.
				return false, fmt.Errorf("project: %s is not valid JSON, leaving it alone: %w", path, jsonErr)
			}
		}
	case !os.IsNotExist(err):
		return false, fmt.Errorf("project: reading %s: %w", path, err)
	}

	if hookPresent(doc) {
		return false, nil
	}
	addHook(doc)

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, fmt.Errorf("project: rendering %s: %w", path, err)
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
		return false, fmt.Errorf("project: creating dir for %s: %w", path, mkErr)
	}
	if wErr := os.WriteFile(path, append(out, '\n'), 0o644); wErr != nil { //nolint:gosec // settings files are user-readable by design
		return false, fmt.Errorf("project: writing %s: %w", path, wErr)
	}
	return true, nil
}

// hookPresent reports whether a SessionStart hook already runs the resolver.
// Matching on the command rather than an exact structure means a user who wired
// the hook themselves, or wrapped it in a shim, is not given a duplicate.
func hookPresent(doc map[string]any) bool {
	return strings.Contains(marshalLoose(doc["hooks"]), HookCommand)
}

// marshalLoose renders any value as JSON text, or "" when it cannot be. Used
// only for substring inspection, so a failure simply means "not present".
func marshalLoose(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// addHook appends weft's SessionStart entry, preserving any hooks already there.
func addHook(doc map[string]any) {
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	entries, _ := hooks["SessionStart"].([]any)
	hooks["SessionStart"] = append(entries, map[string]any{
		"matcher": "startup|resume|clear",
		"hooks": []any{
			map[string]any{"type": "command", "command": HookCommand},
		},
	})
	doc["hooks"] = hooks
}

// DeliverImport writes a managed block of import lines into the project's
// instruction file, returning whether the file changed.
//
// One line per bundle part, pointing inside <repo>/.weft. The lines only change
// when the set of parts changes, so a repository whose rules are stable sees a
// single commit-sized diff once and nothing after that.
func DeliverImport(root, relPath, template string, paths []string) (bool, error) {
	if template == "" {
		template = "@{path}"
	}
	rendered := make([]string, 0, len(paths))
	for _, p := range paths {
		// Relative to the repository root, so the line survives the repository
		// being cloned to a different absolute path, and stays inside the
		// directory an import-path validator will allow.
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		rendered = append(rendered, "./"+filepath.ToSlash(rel))
	}
	return writeManagedBlock(root, relPath, instruction.ImportBody(rendered, template), "")
}

// DeliverInline writes the bundle content itself into the project's instruction
// file, for harnesses with neither a hook nor an include directive.
func DeliverInline(root, relPath, preamble string, parts []Part) (bool, error) {
	sc := make([]instruction.SourceContent, 0, len(parts))
	for _, p := range parts {
		sc = append(sc, instruction.SourceContent{Name: p.Name, Content: p.Content})
	}
	return writeManagedBlock(root, relPath, instruction.InlineBody(sc), preamble)
}

// writeManagedBlock upserts weft's block into a repository file, preserving
// everything outside it byte for byte, and skipping the write when the result
// is identical.
//
// The no-op check matters more here than in the global plane: this file is
// tracked, so an unchanged rewrite would still show as a modified file in git
// status and train the user to ignore weft's diffs.
func writeManagedBlock(root, relPath, body, preamble string) (bool, error) {
	path := filepath.Join(root, filepath.FromSlash(relPath))

	existing, err := os.ReadFile(path) //nolint:gosec // path is inside the repository
	switch {
	case os.IsNotExist(err):
		existing = []byte(preamble)
	case err != nil:
		return false, fmt.Errorf("project: reading %s: %w", path, err)
	}

	updated := instruction.Upsert(existing, body)
	if string(updated) == string(existing) {
		return false, nil
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
		return false, fmt.Errorf("project: creating dir for %s: %w", path, mkErr)
	}
	if wErr := os.WriteFile(path, updated, 0o644); wErr != nil { //nolint:gosec // instruction files are user-readable by design
		return false, fmt.Errorf("project: writing %s: %w", path, wErr)
	}
	return true, nil
}
