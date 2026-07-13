package cmd

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/rules"
)

// ruleSuggestion is a proposed front-matter header for an un-annotated rule file.
type ruleSuggestion struct {
	Label  string
	Detect string
	// Confident is true when the path matched a recognised rule convention (a
	// known stack dir, or common*/doc). When false the file was flagged only
	// because a labeled sibling establishes the directory as a rules dir, and the
	// suggested detect is a best-effort "always" the author should review.
	Confident bool
}

// ruleFinding is one rule-health issue in a source.
type ruleFinding struct {
	File    string // path relative to the source root
	Detail  string // human detail (dangling target, ignored duplicate, …)
	Suggest ruleSuggestion
}

// ruleAudit is the annotation health of a single source's rule tree. Candidates
// counts only files that are rules or that a convention says should be rules —
// documentation and other non-rule .md files are excluded so the ratio is
// meaningful.
type ruleAudit struct {
	Candidates int
	Labeled    int
	Missing    []ruleFinding
	Duplicates []ruleFinding
	Dangling   []ruleFinding
}

// auditSourceRules inspects a source's rule tree for annotation health. It
// reuses collectSourceFiles (which already excludes READMEs, managed dirs and
// project dirs) to enumerate rule-looking files, then reports:
//
//   - missing front-matter: a rule-looking file with no label, flagged only when
//     it sits in a recognised rule location (known stack / common* / doc) or
//     alongside a labeled sibling — so docs and stray files never nag;
//   - duplicate labels within the source (the resolver keeps the first, ignores
//     the rest);
//   - dangling extends: a target naming no label in the source.
func auditSourceRules(root string, managedDirs []string, projectNames map[string]bool, instructionGlob string) (ruleAudit, error) {
	files, err := collectSourceFiles(root, managedDirs, projectNames)
	if err != nil {
		return ruleAudit{}, err
	}

	type parsed struct {
		rel  string
		rule rules.Rule
	}
	all := make([]parsed, 0, len(files))
	labelPath := map[string]string{} // label -> first declaring path
	dirHasLabel := map[string]bool{} // dir -> any labeled file present

	var audit ruleAudit

	// First pass: parse, record labels, detect duplicates.
	for _, f := range files {
		rel, relErr := filepath.Rel(root, f.abs)
		if relErr != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if isInstructionFile(path.Base(rel), instructionGlob) {
			continue // the source's own projection wrapper (CLAUDE.md), not a rule
		}
		r, perr := rules.ParseRule(f.abs)
		if perr != nil {
			continue // unreadable; collectSourceFiles already filtered the tree
		}
		all = append(all, parsed{rel: rel, rule: r})
		if r.Label == "" {
			continue
		}
		audit.Labeled++
		dirHasLabel[path.Dir(rel)] = true
		if first, dup := labelPath[r.Label]; dup {
			audit.Duplicates = append(audit.Duplicates, ruleFinding{
				File:   rel,
				Detail: fmt.Sprintf("label %q already defined by %s — the resolver ignores this file", r.Label, first),
			})
			continue
		}
		labelPath[r.Label] = rel
	}

	// Second pass: missing front-matter and dangling extends.
	for _, p := range all {
		if p.rule.Label == "" {
			sug, kind := suggestRuleFrontMatter(p.rel)
			if kind == "unknown" && !dirHasLabel[path.Dir(p.rel)] {
				continue // not confidently a rule (docs, stray notes) — don't nag
			}
			audit.Missing = append(audit.Missing, ruleFinding{File: p.rel, Suggest: sug})
			continue
		}
		for _, ext := range p.rule.Extends {
			if ext = strings.TrimSpace(ext); ext == "" {
				continue
			}
			if _, ok := labelPath[ext]; !ok {
				audit.Dangling = append(audit.Dangling, ruleFinding{
					File:   p.rel,
					Detail: fmt.Sprintf("extends %q — no rule in this source declares that label", ext),
				})
			}
		}
	}

	audit.Candidates = audit.Labeled + len(audit.Missing)
	return audit, nil
}

// suggestRuleFrontMatter proposes a label and detect predicate for an
// un-annotated file from its path, and returns the match kind
// ("stack" | "common" | "doc" | "unknown"). It keys off the file's IMMEDIATE
// parent directory (and, for common/doc, the filename stem) rather than any
// ancestor — so `java/springboot.md` is a rule but `java/tickets/DIGI-1/x.md` is
// not, and it works for both `dev/<stack>/` and root-level `<stack>/` layouts.
// The label suggestion is the filename stem; the author should shorten it to the
// canonical stack name where appropriate.
func suggestRuleFrontMatter(rel string) (ruleSuggestion, string) {
	stem := strings.TrimSuffix(path.Base(rel), ".md")
	parent := path.Base(path.Dir(rel))

	if sc, ok := knownStacks[parent]; ok {
		return ruleSuggestion{Label: stem, Detect: detectPredicate(sc.files), Confident: true}, "stack"
	}
	if strings.HasPrefix(parent, "common") || strings.HasPrefix(stem, "common") {
		return ruleSuggestion{Label: stem, Detect: "true", Confident: true}, "common"
	}
	if parent == "doc" || stem == "doc" {
		return ruleSuggestion{Label: stem, Detect: "true", Confident: true}, "doc"
	}
	return ruleSuggestion{Label: stem, Detect: "true", Confident: false}, "unknown"
}

// isInstructionFile reports whether base is the source's own projection wrapper
// (its instruction file, e.g. CLAUDE.md) rather than a rule. Only a plain
// filename glob is treated this way; a real glob (**/*.md) means the source
// inlines its whole tree and the exclusion does not apply.
func isInstructionFile(base, glob string) bool {
	if glob == "" || strings.ContainsAny(glob, "*?[") {
		return false
	}
	return base == glob
}

// detectPredicate builds a CEL "any of these files is present" predicate.
func detectPredicate(files []string) string {
	parts := make([]string, 0, len(files))
	for _, f := range files {
		parts = append(parts, fmt.Sprintf("'%s' in files", f))
	}
	return strings.Join(parts, " || ")
}

// reportRuleHealth writes the rule-annotation health of every registered source
// to w: sources contributing nothing to the resolver, files missing front-matter
// (with suggested headers), duplicate labels, and dangling extends. Read-only —
// suggestions are printed for the author to apply, never written automatically.
func reportRuleHealth(w io.Writer) {
	reg, err := newRegistry()
	if err != nil {
		return
	}
	srcs, err := reg.List()
	if err != nil || len(srcs) == 0 {
		return
	}

	activeSet := map[string]bool{}
	if active := activeProfileName(); active != "" {
		if pm, perr := newProfileManager(); perr == nil {
			if p, gerr := pm.Get(active); gerr == nil {
				for _, name := range p.Sources {
					activeSet[name] = true
				}
			}
		}
	}

	type srcReport struct {
		name   string
		active bool
		audit  ruleAudit
	}
	var reports []srcReport
	for _, s := range srcs {
		audit, aerr := auditSourceRules(
			locate.ExpandHome(s.Root),
			s.Structure.ManagedDirs(),
			buildNameSet(s.Structure.EffectiveProjectDirNames()),
			s.Structure.InstructionGlob,
		)
		if aerr != nil || audit.Candidates == 0 {
			continue // unreadable, or not a rules source
		}
		if len(audit.Missing) == 0 && len(audit.Duplicates) == 0 && len(audit.Dangling) == 0 {
			continue // healthy
		}
		reports = append(reports, srcReport{name: s.Name, active: activeSet[s.Name], audit: audit})
	}
	if len(reports) == 0 {
		return
	}

	fmt.Fprintln(w, "\nRule annotations:")
	for _, r := range reports {
		tag := ""
		if r.active {
			tag = " (active profile)"
		}
		if r.audit.Labeled == 0 {
			fmt.Fprintf(w, "  ✗ source %q%s: none of its %d rule file(s) are annotated — contributes nothing to the resolver\n",
				r.name, tag, r.audit.Candidates)
		} else {
			fmt.Fprintf(w, "  source %q%s — %d/%d rule file(s) annotated\n",
				r.name, tag, r.audit.Labeled, r.audit.Candidates)
		}
		for _, f := range r.audit.Missing {
			label := "suggest"
			if !f.Suggest.Confident {
				label = "review "
			}
			fmt.Fprintf(w, "    ✎ %s  (missing front-matter)\n", f.File)
			fmt.Fprintf(w, "        %s → label: %s   detect: %q\n", label, f.Suggest.Label, f.Suggest.Detect)
		}
		for _, f := range r.audit.Duplicates {
			fmt.Fprintf(w, "    ⚠ %s  %s\n", f.File, f.Detail)
		}
		for _, f := range r.audit.Dangling {
			fmt.Fprintf(w, "    ⚠ %s  %s\n", f.File, f.Detail)
		}
	}
	fmt.Fprintln(w, "  fix: add the suggested front-matter (adjust label/detect as needed), then re-run 'weft profile use'.")
}
