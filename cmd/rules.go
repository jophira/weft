package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/rules"
	"github.com/jophira/weft/internal/source"
)

var (
	rulesRoot       string
	rulesShowManife bool
	rulesNoCache    bool
	rulesRebuild    bool
	rulesCachePath  string
	rulesRecord     bool

	rulesBuildRoot   string
	rulesBuildOutput string
)

var rulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "Convention-driven rules resolution",
	Long: `Resolve which rule files apply to a repository based on its signals
(files present, declared dependencies) and their declared dependencies.

Distinct from "weft resolve", which reverse-looks-up the source that produced a
target file.`,
}

var rulesResolveCmd = &cobra.Command{
	Use:   "resolve [repo-path]",
	Short: "Select and assemble the rules that apply to a repository",
	Long: `Inspect a repository, evaluate each rule's front-matter detect predicate
against it, expand matches across their extends dependencies, and print the
assembled rule bundle in deterministic load order.

The repo path defaults to the current directory. The rules tree is taken from
--rules-root, or — when that is omitted — from the sources of the active
profile, resolved in priority order and layered (higher priority last). A
signals.yaml cache in each rules tree is used when fresh and rebuilt when stale;
the cache only affects speed, never the result.

With no arguments at all, "weft rules resolve" inspects the current directory
using the active profile and writes the bundle to stdout — the form a session
hook invokes.

Examples:
  weft rules resolve
  weft rules resolve . --rules-root ~/weft-sources/ai-rules-personal-tech/dev
  weft rules resolve /path/to/repo --manifest`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath := "."
		if len(args) == 1 {
			repoPath = args[0]
		}
		repoAbs, err := expandAndAbs(repoPath)
		if err != nil {
			return fmt.Errorf("resolving repo path: %w", err)
		}

		roots, profileName, err := resolveRootSpecs()
		if err != nil {
			return err
		}

		opts := rules.CacheOptions{Disabled: rulesNoCache, ForceRebuild: rulesRebuild}
		if rulesRoot != "" && rulesCachePath != "" {
			if opts.Path, err = expandAndAbs(rulesCachePath); err != nil {
				return fmt.Errorf("resolving cache path: %w", err)
			}
		}

		ress, err := resolveAcrossRoots(repoAbs, roots, opts)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		if rulesShowManife {
			if err := printResolveManifest(out, ress, repoAbs, profileName); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(out, layerBundles(ress))
		}

		// Persistence is opt-in and best-effort: it must never corrupt the
		// stdout bundle a hook consumes, nor fail the resolve.
		if rulesRecord {
			if err := recordResolve(repoAbs, profileName, ress); err != nil {
				fmt.Fprintf(os.Stderr, "weft: could not record resolve: %v\n", err)
			}
		}
		return nil
	},
}

// recordResolve persists an audit record of the resolve to the repo's
// .weft/ logs and the global monthly rollup.
func recordResolve(repoAbs, profileName string, ress []sourceResolution) error {
	rec := buildResolveRecord(repoAbs, profileName, ress, time.Now().UTC())

	targets := rules.RecordTargets{
		RepoLog: filepath.Join(repoAbs, ".weft", "resolve.log.jsonl"),
		Latest:  filepath.Join(repoAbs, ".weft", "resolve.latest.json"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		targets.GlobalLog = filepath.Join(home, ".weft", "audit", rec.Timestamp.Format("2006-01")+".jsonl")
	}

	_, err := rules.PersistRecord(rec, targets)
	return err
}

// buildResolveRecord converts the per-source resolutions into a combined audit
// record.
func buildResolveRecord(repoAbs, profileName string, ress []sourceResolution, now time.Time) rules.ResolveRecord {
	parts := make([]rules.RecordPart, 0, len(ress))
	for _, r := range ress {
		parts = append(parts, rules.RecordPart{Source: r.Source, Res: r.Res})
	}
	return rules.NewResolveRecord(repoAbs, profileName, parts, now)
}

var rulesBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Generate the signals.yaml resolution cache for a rules tree",
	Long: `Walk a rules tree, parse every rule's front-matter, and write a
pre-resolved signals.yaml cache so subsequent "weft rules resolve" runs skip the
tree walk. The cache is an optimization only: resolve self-heals a stale or
missing cache, so building by hand is never required for correctness.

Examples:
  weft rules build --rules-root ./dev
  weft rules build --rules-root ./dev -o /tmp/signals.yaml`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if rulesBuildRoot == "" {
			return fmt.Errorf("--rules-root is required")
		}
		rootAbs, err := expandAndAbs(rulesBuildRoot)
		if err != nil {
			return fmt.Errorf("resolving rules root: %w", err)
		}
		cache, skipped, err := rules.BuildCache(rootAbs, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("building cache: %w", err)
		}
		outPath := rules.DefaultCachePath(rootAbs)
		if rulesBuildOutput != "" {
			if outPath, err = expandAndAbs(rulesBuildOutput); err != nil {
				return fmt.Errorf("resolving output path: %w", err)
			}
		}
		if err := cache.Save(outPath); err != nil {
			return fmt.Errorf("writing cache: %w", err)
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "wrote %d label(s) to %s\n", len(cache.Labels), outPath)
		for _, s := range skipped {
			fmt.Fprintf(out, "  skipped %s: %s\n", s.Path, s.Reason)
		}
		return nil
	},
}

// namedRoot is a rules tree to resolve, optionally tagged with the profile
// source it came from (empty for an explicit --rules-root).
type namedRoot struct {
	Name string
	Root string
}

// sourceResolution is one rules tree's resolution result.
type sourceResolution struct {
	Source string
	Root   string
	Res    rules.Resolution
	Status rules.CacheStatus
}

// resolveRootSpecs determines which rules tree(s) to resolve: the explicit
// --rules-root when set, otherwise the active profile's sources in priority
// order. profileName is empty in the explicit-root case.
func resolveRootSpecs() (roots []namedRoot, profileName string, err error) {
	if rulesRoot != "" {
		rootAbs, absErr := expandAndAbs(rulesRoot)
		if absErr != nil {
			return nil, "", fmt.Errorf("resolving rules root: %w", absErr)
		}
		return []namedRoot{{Root: rootAbs}}, "", nil
	}

	profileName = activeProfileName()
	if profileName == "" {
		return nil, "", fmt.Errorf("no active profile; pass --rules-root or run 'weft profile use <name>'")
	}
	srcs, srcErr := profileSourcesByPriority(profileName)
	if srcErr != nil {
		return nil, "", srcErr
	}
	if len(srcs) == 0 {
		return nil, "", fmt.Errorf("active profile %q has no resolvable sources", profileName)
	}
	roots = make([]namedRoot, 0, len(srcs))
	for _, s := range srcs {
		roots = append(roots, namedRoot{Name: s.Name, Root: locate.ExpandHome(s.Root)})
	}
	return roots, profileName, nil
}

// profileSourcesByPriority returns the profile's registered sources sorted into
// priority (low→high) order. Sources named by the profile but absent from the
// registry are skipped with a warning.
func profileSourcesByPriority(profileName string) ([]source.Source, error) {
	pm, err := newProfileManager()
	if err != nil {
		return nil, err
	}
	p, err := pm.Get(profileName)
	if err != nil {
		return nil, fmt.Errorf("loading profile %q: %w", profileName, err)
	}
	reg, err := newRegistry()
	if err != nil {
		return nil, err
	}
	all, err := reg.List()
	if err != nil {
		return nil, fmt.Errorf("listing sources: %w", err)
	}
	byName := make(map[string]source.Source, len(all))
	for _, s := range all {
		byName[s.Name] = s
	}
	srcs := make([]source.Source, 0, len(p.Sources))
	for _, name := range p.Sources {
		if s, ok := byName[name]; ok {
			srcs = append(srcs, s)
		} else {
			fmt.Fprintf(os.Stderr, "weft: profile source %q not registered; skipping\n", name)
		}
	}
	source.SortByPriority(srcs)
	return srcs, nil
}

// resolveAcrossRoots resolves each root against the repo in order. A failure on
// an explicit (unnamed) root is fatal; a failure on a named profile source is
// skipped with a warning so one bad source cannot abort the whole resolve.
func resolveAcrossRoots(repoAbs string, roots []namedRoot, opts rules.CacheOptions) ([]sourceResolution, error) {
	ctx, err := rules.BuildContext(repoAbs)
	if err != nil {
		return nil, fmt.Errorf("inspecting repo %s: %w", repoAbs, err)
	}
	ev, err := rules.NewCELEvaluator()
	if err != nil {
		return nil, err
	}

	out := make([]sourceResolution, 0, len(roots))
	for _, nr := range roots {
		res, status, resErr := rules.ResolveWithCache(nr.Root, ctx, ev, opts)
		if resErr != nil {
			if nr.Name == "" {
				return nil, fmt.Errorf("resolving rules: %w", resErr)
			}
			fmt.Fprintf(os.Stderr, "weft: skipping source %q: %v\n", nr.Name, resErr)
			continue
		}
		out = append(out, sourceResolution{Source: nr.Name, Root: nr.Root, Res: res, Status: status})
	}
	return out, nil
}

// layerBundles concatenates the per-source bundles in resolution (priority)
// order, separated by blank lines, dropping empties.
func layerBundles(ress []sourceResolution) string {
	parts := make([]string, 0, len(ress))
	for _, r := range ress {
		if b := r.Res.Bundle(); b != "" {
			parts = append(parts, b)
		}
	}
	return strings.Join(parts, "\n\n")
}

// profileResolveManifest wraps per-source manifests for a profile-derived
// resolve.
type profileResolveManifest struct {
	GeneratedAt time.Time        `json:"generated_at"`
	RepoRoot    string           `json:"repo_root"`
	Profile     string           `json:"profile"`
	Sources     []rules.Manifest `json:"sources"`
}

// printResolveManifest emits the JSON audit manifest. A single explicit
// --rules-root prints the flat per-tree manifest; a profile-derived resolve
// prints the wrapped per-source form.
func printResolveManifest(out io.Writer, ress []sourceResolution, repoAbs, profileName string) error {
	now := time.Now().UTC()
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")

	if profileName == "" && len(ress) == 1 {
		m := rules.NewManifest(ress[0].Res, ress[0].Root, repoAbs, now).WithCache(ress[0].Status)
		return enc.Encode(m)
	}

	wrap := profileResolveManifest{GeneratedAt: now, RepoRoot: repoAbs, Profile: profileName}
	for _, r := range ress {
		wrap.Sources = append(wrap.Sources, rules.NewManifest(r.Res, r.Root, repoAbs, now).WithCache(r.Status))
	}
	return enc.Encode(wrap)
}

func init() {
	rulesResolveCmd.Flags().StringVar(&rulesRoot, "rules-root", "", "rules tree to resolve against (default: active profile's sources)")
	rulesResolveCmd.Flags().BoolVar(&rulesShowManife, "manifest", false, "print the JSON audit manifest instead of the assembled bundle")
	rulesResolveCmd.Flags().BoolVar(&rulesNoCache, "no-cache", false, "bypass the signals.yaml cache and resolve from the tree")
	rulesResolveCmd.Flags().BoolVar(&rulesRebuild, "rebuild-cache", false, "ignore any existing cache and regenerate it")
	rulesResolveCmd.Flags().StringVar(&rulesCachePath, "cache", "", "cache file path (only with --rules-root; default: <rules-root>/signals.yaml)")
	rulesResolveCmd.Flags().BoolVar(&rulesRecord, "record", false, "append a deduped audit record to <repo>/.weft/ and the global ~/.weft/audit rollup")

	rulesBuildCmd.Flags().StringVar(&rulesBuildRoot, "rules-root", "", "path to the rules tree to index (required)")
	rulesBuildCmd.Flags().StringVarP(&rulesBuildOutput, "output", "o", "", "cache output path (default: <rules-root>/signals.yaml)")

	rulesCmd.AddCommand(rulesResolveCmd)
	rulesCmd.AddCommand(rulesBuildCmd)
	rootCmd.AddCommand(rulesCmd)
}
