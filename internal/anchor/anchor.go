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

// Has reports whether content contains any weft anchor token.
func Has(content []byte) bool {
	s := string(content)
	return strings.Contains(s, RootToken) ||
		strings.Contains(s, HomeToken) ||
		strings.Contains(s, DocsToken) ||
		sourceTokenRe.MatchString(s)
}
