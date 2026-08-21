// Package anchor expands weft path anchors in rule/command/agent content.
//
// Sources reference other files with machine-independent tokens instead of
// hardcoded absolute paths, so the same files work wherever the source is
// cloned. weft expands the tokens at projection time to real paths on this
// machine:
//
//	{{weft.root}}          -> the current source's root
//	{{weft.source:NAME}}   -> the root of the named source NAME
//	{{weft.home}}          -> the weft workbench root (~/weft)
//	{{weft.docs}}          -> the docs home (~/docs or ~/weft/docs after adopt)
//
// Example: `@{{weft.root}}/common/code-review.md` becomes
// `@/home/you/weft/sources/work/common/code-review.md` on projection. Relocating
// a source (or the docs home) is then just re-registering it — no file edits.
package anchor

import (
	"regexp"
	"sort"
	"strings"
)

// Anchor tokens. Each is a path-template placeholder, not a credential
// (silences gosec G101 on the *Token constants).
const (
	RootToken = "{{weft.root}}" //nolint:gosec // G101 false positive: path-template placeholder
	HomeToken = "{{weft.home}}" //nolint:gosec // G101 false positive: path-template placeholder
	DocsToken = "{{weft.docs}}" //nolint:gosec // G101 false positive: path-template placeholder
)

// sourceTokenRe matches {{weft.source:NAME}} and captures NAME.
var sourceTokenRe = regexp.MustCompile(`\{\{weft\.source:([^}]+)\}\}`)

// Anchors carries the expansion targets. Root is per-source; Home and Docs are
// machine-global; ByName resolves {{weft.source:NAME}}. All values should be
// absolute, home-expanded paths. Any zero-valued field leaves its token
// untouched so the unresolved reference stays visible (catchable by
// `weft doctor`) instead of silently expanding to an empty path.
type Anchors struct {
	Root   string
	Home   string
	Docs   string
	ByName map[string]string
}

// Expand replaces weft anchors in content per a. A {{weft.source:NAME}} whose
// NAME is not in a.ByName is left untouched (kept visible for `weft doctor`).
func Expand(content []byte, a Anchors) []byte {
	if !Has(content) {
		return content
	}
	s := string(content)
	if a.Root != "" {
		s = strings.ReplaceAll(s, RootToken, a.Root)
	}
	if a.Home != "" {
		s = strings.ReplaceAll(s, HomeToken, a.Home)
	}
	if a.Docs != "" {
		s = strings.ReplaceAll(s, DocsToken, a.Docs)
	}
	s = sourceTokenRe.ReplaceAllStringFunc(s, func(match string) string {
		name := strings.TrimSpace(sourceTokenRe.FindStringSubmatch(match)[1])
		if root, ok := a.ByName[name]; ok {
			return root
		}
		return match // unresolved — leave visible
	})
	return []byte(s)
}

// Collapse reverses Expand against the same Anchors: absolute paths matching
// an anchor's target are turned back into their token form, so content
// written back to a source stays machine-independent instead of landing with
// a hardcoded path where an anchor used to be (#259). A path matching no
// anchor is left untouched.
//
// Anchors can overlap — Docs nested under Home, or one source's root nested
// under another's — so the longest matching path is collapsed first; a
// shorter, less specific match processed first would swallow part of a
// longer one before it ever got a chance to match. Where two anchors resolve
// to the exact same path (a source registered at {{weft.root}}'s own root),
// {{weft.root}} wins over naming that source explicitly, since a source
// should self-reference rather than name itself.
func Collapse(content []byte, a Anchors) []byte {
	type candidate struct {
		path  string
		token string
	}
	var candidates []candidate
	if a.Root != "" {
		candidates = append(candidates, candidate{a.Root, RootToken})
	}
	if a.Home != "" {
		candidates = append(candidates, candidate{a.Home, HomeToken})
	}
	if a.Docs != "" {
		candidates = append(candidates, candidate{a.Docs, DocsToken})
	}
	names := make([]string, 0, len(a.ByName))
	for name := range a.ByName {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic order for equal-length ties
	for _, name := range names {
		if path := a.ByName[name]; path != "" {
			candidates = append(candidates, candidate{path, "{{weft.source:" + name + "}}"})
		}
	}

	// Longest path first; among equal-length paths, the order built above
	// (Root, Home, Docs, then names alphabetically) is preserved by a stable
	// sort, giving Root priority on a same-path collision.
	sort.SliceStable(candidates, func(i, j int) bool {
		return len(candidates[i].path) > len(candidates[j].path)
	})

	s := string(content)
	for _, c := range candidates {
		s = strings.ReplaceAll(s, c.path, c.token)
	}
	return []byte(s)
}

// Has reports whether content contains any weft anchor token.
func Has(content []byte) bool {
	s := string(content)
	return strings.Contains(s, RootToken) ||
		strings.Contains(s, HomeToken) ||
		strings.Contains(s, DocsToken) ||
		sourceTokenRe.MatchString(s)
}
