// Package rules implements weft's convention-driven rules resolver: given a
// repository's signals (files present, declared dependencies) it selects which
// rule files from a rules tree should be loaded, walks their declared
// dependencies, and assembles them into a single deterministic bundle plus an
// audit manifest.
//
// This is a conditional-selection layer on top of the unconditional assembly in
// package collect: collect concatenates *everything* matching a glob, whereas
// rules concatenates only the subset whose front-matter `detect` predicate
// matches the repo (plus everything those rules `extends`).
//
// Every entry point is total: malformed front-matter, invalid predicates and
// dangling `extends` references are recorded in the Resolution rather than
// raised as fatal errors, so a single bad rule file never aborts a resolve.
package rules

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// FrontMatter is the YAML metadata block at the top of a rule file. It is the
// single authored source of truth for how a rule is detected and wired —
// there is no separate central registry (cf. the design's decision to kill an
// authored signals.yaml).
type FrontMatter struct {
	// Label is the rule's stable identity, referenced by other rules' Extends.
	// A file with no Label does not participate in resolution.
	Label string `yaml:"label"`
	// Detect is a CEL boolean predicate over the repo Context (variables
	// `files` and `deps`). Empty means the rule is dependency-only: it never
	// auto-matches and loads solely when another matched rule Extends it.
	Detect string `yaml:"detect"`
	// Extends names the rules this rule depends on; they are pulled in
	// transitively and ordered before this rule.
	Extends []string `yaml:"extends"`
	// Priority breaks ties between independent rules. Higher numbers are
	// emitted later so they win on conflict — consistent with
	// source.SortByPriority and the cascade/last-wins overlay. cf. Java: a
	// Comparator key applied with a stable sort.
	Priority int `yaml:"priority"`
}

// Rule is a parsed rule file: its front-matter metadata plus the markdown body
// that gets injected when the rule is loaded.
type Rule struct {
	FrontMatter
	// Path is the absolute path the rule was read from.
	Path string
	// Body is the file content with the front-matter block stripped.
	Body string
}

const frontMatterDelimiter = "---"

// ParseRule reads and parses the rule file at path. A file without a valid
// front-matter block parses cleanly with a zero FrontMatter and the whole file
// as Body — such a rule is simply ignored by the resolver (no Label).
func ParseRule(path string) (Rule, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from a walked rules tree, not user input
	if err != nil {
		return Rule{}, err
	}
	yamlPart, body := splitFrontMatter(data)
	r := Rule{Path: path, Body: body}
	if yamlPart == "" {
		return r, nil
	}
	// A malformed YAML header is non-fatal: keep the body, drop the metadata,
	// so one bad file cannot break a whole resolve.
	if err := yaml.Unmarshal([]byte(yamlPart), &r.FrontMatter); err != nil {
		return Rule{Path: path, Body: body}, nil //nolint:nilerr // intentional: degrade to body-only
	}
	r.Label = strings.TrimSpace(r.Label)
	r.Detect = strings.TrimSpace(r.Detect)
	return r, nil
}

// splitFrontMatter separates a leading `---` fenced YAML block from the markdown
// body. It returns ("", wholeFile) when the file does not open with a fence or
// the fence is never closed. Delimiter detection is CRLF-tolerant.
func splitFrontMatter(data []byte) (yamlPart, body string) {
	text := string(data)
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != frontMatterDelimiter {
		return "", text
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == frontMatterDelimiter {
			yamlPart = strings.Join(lines[1:i], "\n")
			body = strings.Join(lines[i+1:], "\n")
			return yamlPart, strings.TrimLeft(body, "\n")
		}
	}
	// Unterminated fence: treat the entire file as body.
	return "", text
}
