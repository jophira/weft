package rules

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// ruleFileExt is the extension of files considered as rule candidates.
const ruleFileExt = ".md"

// LoadedRule is a rule selected by a resolve, in final load order.
type LoadedRule struct {
	Label string
	Path  string
	// Direct is true when the rule matched its own Detect predicate, false when
	// it was pulled in solely as another rule's Extends dependency.
	Direct   bool
	Priority int
	Body     string
}

// SkippedRule records a rule that was discovered but not loaded, with the
// reason — the audit trail for "why isn't X here?".
type SkippedRule struct {
	// Label is the rule's label, or its path when the problem is a duplicate or
	// undetermined label.
	Label  string
	Path   string
	Reason string
}

// Resolution is the total result of resolving a rules tree against a repo
// Context: the assembled bundle plus the full account of what loaded, what was
// skipped, and which Extends references dangled.
type Resolution struct {
	// Loaded is the selected rules in deterministic load order
	// (dependencies before dependents; priority then path as tie-breaks).
	Loaded []LoadedRule
	// Skipped explains rules that were discovered but not loaded.
	Skipped []SkippedRule
	// UnknownExtends lists Extends targets referenced by a loaded rule that do
	// not correspond to any rule in the tree, sorted and de-duplicated.
	UnknownExtends []string
}

// Bundle concatenates the loaded rule bodies in order, separated by blank lines.
func (r Resolution) Bundle() string {
	parts := make([]string, 0, len(r.Loaded))
	for _, lr := range r.Loaded {
		if body := strings.Trim(lr.Body, "\n"); body != "" {
			parts = append(parts, body)
		}
	}
	return strings.Join(parts, "\n\n")
}

// Resolve loads every rule under rulesRoot, evaluates each rule's Detect
// predicate against ctx, expands matches across their Extends dependencies, and
// returns the ordered selection. It never fails on individual bad rules; those
// surface in Resolution.Skipped / UnknownExtends. An error is returned only when
// the tree itself cannot be walked.
func Resolve(rulesRoot string, ctx Context, ev Evaluator) (Resolution, error) {
	rules, skipped, err := loadTree(rulesRoot)
	if err != nil {
		return Resolution{}, err
	}

	byLabel := make(map[string]Rule, len(rules))
	for _, r := range rules {
		byLabel[r.Label] = r
	}

	// Phase 1: direct matches from Detect predicates.
	direct := make(map[string]bool)
	for _, r := range rules {
		matched, evalErr := ev.Eval(r.Detect, ctx)
		if evalErr != nil {
			skipped = append(skipped, SkippedRule{Label: r.Label, Path: r.Path, Reason: evalErr.Error()})
			continue
		}
		if matched {
			direct[r.Label] = true
		}
	}

	// Phase 2: transitive closure over Extends.
	required, unknown := closure(direct, byLabel)

	// Phase 3: deterministic ordering (dependencies first).
	ordered := topoOrder(required, byLabel)

	loaded := make([]LoadedRule, 0, len(ordered))
	for _, label := range ordered {
		r := byLabel[label]
		loaded = append(loaded, LoadedRule{
			Label:    r.Label,
			Path:     r.Path,
			Direct:   direct[label],
			Priority: r.Priority,
			Body:     r.Body,
		})
	}

	return Resolution{Loaded: loaded, Skipped: skipped, UnknownExtends: sortedKeys(unknown)}, nil
}

// loadTree walks rulesRoot, parsing every *.md file. Only files declaring a
// label participate; files without one are silently ignored. Duplicate labels
// keep the first (by sorted path) and report the rest as skipped.
func loadTree(rulesRoot string) (rules []Rule, skipped []SkippedRule, err error) {
	var paths []string
	walkErr := filepath.WalkDir(rulesRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ruleFileExt {
			paths = append(paths, path)
		}
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	sort.Strings(paths) // deterministic order so duplicate-label resolution is stable

	seen := make(map[string]string) // label -> winning path
	for _, p := range paths {
		r, parseErr := ParseRule(p)
		if parseErr != nil {
			skipped = append(skipped, SkippedRule{Path: p, Reason: parseErr.Error()})
			continue
		}
		if r.Label == "" {
			continue // not part of the convention graph
		}
		if first, dup := seen[r.Label]; dup {
			skipped = append(skipped, SkippedRule{
				Label:  r.Label,
				Path:   p,
				Reason: "duplicate label; already defined by " + first,
			})
			continue
		}
		seen[r.Label] = p
		rules = append(rules, r)
	}
	return rules, skipped, nil
}

// closure expands the directly-matched labels with everything they transitively
// Extend. Extends targets that name no known rule are collected in unknown.
func closure(direct map[string]bool, byLabel map[string]Rule) (required, unknown map[string]bool) {
	required = make(map[string]bool)
	unknown = make(map[string]bool)
	var visit func(label string)
	visit = func(label string) {
		if required[label] {
			return
		}
		required[label] = true
		for _, dep := range byLabel[label].Extends {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if _, ok := byLabel[dep]; !ok {
				unknown[dep] = true
				continue
			}
			visit(dep)
		}
	}
	for label := range direct {
		visit(label)
	}
	return required, unknown
}

// topoOrder returns the required labels with dependencies before dependents.
// Among labels that are ready simultaneously, ordering is by ascending Priority
// then path, so higher-priority rules land later (last-wins). Any labels left in
// a cycle are appended in the same deterministic tie-break order.
func topoOrder(required map[string]bool, byLabel map[string]Rule) []string {
	indegree := make(map[string]int, len(required))
	dependents := make(map[string][]string, len(required)) // dep -> labels needing it
	for label := range required {
		for _, dep := range byLabel[label].Extends {
			if required[dep] {
				indegree[label]++
				dependents[dep] = append(dependents[dep], label)
			}
		}
	}

	less := func(a, b string) bool {
		ra, rb := byLabel[a], byLabel[b]
		if ra.Priority != rb.Priority {
			return ra.Priority < rb.Priority
		}
		return ra.Path < rb.Path
	}

	ready := make([]string, 0, len(required))
	for label := range required {
		if indegree[label] == 0 {
			ready = append(ready, label)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return less(ready[i], ready[j]) })

	out := make([]string, 0, len(required))
	for len(ready) > 0 {
		next := ready[0]
		ready = ready[1:]
		out = append(out, next)
		for _, dep := range dependents[next] {
			indegree[dep]--
			if indegree[dep] == 0 {
				ready = insertSorted(ready, dep, less)
			}
		}
	}

	// Cycle remnant: emit any unprocessed labels deterministically.
	if len(out) < len(required) {
		remaining := make([]string, 0, len(required)-len(out))
		emitted := make(map[string]bool, len(out))
		for _, l := range out {
			emitted[l] = true
		}
		for label := range required {
			if !emitted[label] {
				remaining = append(remaining, label)
			}
		}
		sort.Slice(remaining, func(i, j int) bool { return less(remaining[i], remaining[j]) })
		out = append(out, remaining...)
	}
	return out
}

// insertSorted inserts label into the already-sorted slice, preserving order.
func insertSorted(s []string, label string, less func(a, b string) bool) []string {
	i := sort.Search(len(s), func(i int) bool { return less(label, s[i]) })
	s = append(s, "")
	copy(s[i+1:], s[i:])
	s[i] = label
	return s
}

// sortedKeys returns the keys of set, sorted.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
