package rules

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ruleFileExt is the extension of files considered as rule candidates.
const ruleFileExt = ".md"

// ruleMeta is everything resolution needs about a rule *except* its body:
// detection, wiring and ordering. Bodies are fetched lazily and only for the
// rules that end up loaded, so the cache path never has to read the whole tree.
type ruleMeta struct {
	Label    string
	Detect   string
	Extends  []string
	Priority int
	Path     string
}

func metaOf(r Rule) ruleMeta {
	return ruleMeta{Label: r.Label, Detect: r.Detect, Extends: r.Extends, Priority: r.Priority, Path: r.Path}
}

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
	return resolveRulesInMemory(rules, skipped, ctx, ev)
}

// resolveRulesInMemory resolves an already-parsed rule set, serving bodies from
// the in-memory Rule values (no re-read).
func resolveRulesInMemory(rules []Rule, skipped []SkippedRule, ctx Context, ev Evaluator) (Resolution, error) {
	metas := make([]ruleMeta, 0, len(rules))
	bodies := make(map[string]string, len(rules))
	for _, r := range rules {
		metas = append(metas, metaOf(r))
		bodies[r.Path] = r.Body
	}
	return resolveMeta(metas, skipped, ctx, ev, func(path string) (string, error) {
		return bodies[path], nil
	})
}

// resolveMeta is the shared resolution core: detect → closure → order → load.
// bodyOf supplies a loaded rule's body by path; both the in-memory and cache
// paths funnel through here so they cannot diverge. The skipped slice is seeded
// by the caller (e.g. duplicate-label reports from loadTree) and extended with
// predicate-evaluation failures.
func resolveMeta(metas []ruleMeta, skipped []SkippedRule, ctx Context, ev Evaluator, bodyOf func(path string) (string, error)) (Resolution, error) {
	byLabel := make(map[string]ruleMeta, len(metas))
	for _, m := range metas {
		byLabel[m.Label] = m
	}

	// Phase 1: direct matches from Detect predicates.
	direct := make(map[string]bool)
	for _, m := range metas {
		matched, evalErr := ev.Eval(m.Detect, ctx)
		if evalErr != nil {
			skipped = append(skipped, SkippedRule{Label: m.Label, Path: m.Path, Reason: evalErr.Error()})
			continue
		}
		if matched {
			direct[m.Label] = true
		}
	}

	// Phase 2: transitive closure over Extends.
	required, unknown := closure(direct, byLabel)

	// Phase 3: deterministic ordering (dependencies first).
	ordered := topoOrder(required, byLabel)

	loaded := make([]LoadedRule, 0, len(ordered))
	for _, label := range ordered {
		m := byLabel[label]
		body, err := bodyOf(m.Path)
		if err != nil {
			skipped = append(skipped, SkippedRule{Label: label, Path: m.Path, Reason: err.Error()})
			continue
		}
		loaded = append(loaded, LoadedRule{
			Label:    m.Label,
			Path:     m.Path,
			Direct:   direct[label],
			Priority: m.Priority,
			Body:     body,
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
func closure(direct map[string]bool, byLabel map[string]ruleMeta) (required, unknown map[string]bool) {
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
func topoOrder(required map[string]bool, byLabel map[string]ruleMeta) []string {
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

// readFileBody reads path and returns its content with any front-matter block
// stripped. Used by the cache path to fetch bodies of loaded rules on demand.
func readFileBody(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from the cache's own file list
	if err != nil {
		return "", err
	}
	_, body := splitFrontMatter(data)
	return body, nil
}
