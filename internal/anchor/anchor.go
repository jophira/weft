// Package anchor expands weft path anchors in rule/command/agent content.
//
// Sources reference other files with machine-independent tokens instead of
// hardcoded absolute paths, so the same files work wherever the source is
// cloned. weft expands the tokens at projection time to the registered source
// roots:
//
//	{{weft.root}}          -> the current source's root
//	{{weft.source:NAME}}   -> the root of the named source NAME
//
// Example: `@{{weft.root}}/common/code-review.md` becomes
// `@/home/you/rules/work/common/code-review.md` on projection. Relocating a
// source is then just re-registering it — no file edits.
package anchor

import (
	"regexp"
	"strings"
)

// RootToken is the anchor for the current source's own root.
const RootToken = "{{weft.root}}" //nolint:gosec // G101 false positive: a path-template placeholder, not a credential

// sourceTokenRe matches {{weft.source:NAME}} and captures NAME.
var sourceTokenRe = regexp.MustCompile(`\{\{weft\.source:([^}]+)\}\}`)

// Expand replaces weft anchors in content. {{weft.root}} becomes selfRoot;
// {{weft.source:NAME}} becomes byName[NAME]. selfRoot and the byName values
// should be absolute, home-expanded paths.
//
// A {{weft.source:NAME}} whose NAME is not in byName is left untouched so the
// unresolved reference stays visible (and is catchable by `weft doctor`) rather
// than silently expanding to an empty path. When selfRoot is empty, the root
// token is likewise left untouched.
func Expand(content []byte, selfRoot string, byName map[string]string) []byte {
	if !Has(content) {
		return content
	}
	s := string(content)
	if selfRoot != "" {
		s = strings.ReplaceAll(s, RootToken, selfRoot)
	}
	s = sourceTokenRe.ReplaceAllStringFunc(s, func(match string) string {
		name := strings.TrimSpace(sourceTokenRe.FindStringSubmatch(match)[1])
		if root, ok := byName[name]; ok {
			return root
		}
		return match // unresolved — leave visible
	})
	return []byte(s)
}

// Has reports whether content contains any weft anchor token.
func Has(content []byte) bool {
	return strings.Contains(string(content), RootToken) || sourceTokenRe.Match(content)
}
