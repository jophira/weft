package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/rules"
)

var (
	rulesRoot       string
	rulesShowManife bool
	rulesNoCache    bool
	rulesRebuild    bool
	rulesCachePath  string

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

The repo path defaults to the current directory. The rules tree is given by
--rules-root. A signals.yaml cache in the rules tree is used automatically when
fresh and rebuilt when stale; the cache only affects speed, never the result.

Examples:
  weft rules resolve . --rules-root ~/weft-sources/ai-rules-personal-tech/dev
  weft rules resolve /path/to/repo --rules-root ./dev --manifest
  weft rules resolve . --rules-root ./dev --no-cache`,
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
		if rulesRoot == "" {
			return fmt.Errorf("--rules-root is required")
		}
		rootAbs, err := expandAndAbs(rulesRoot)
		if err != nil {
			return fmt.Errorf("resolving rules root: %w", err)
		}

		ctx, err := rules.BuildContext(repoAbs)
		if err != nil {
			return fmt.Errorf("inspecting repo %s: %w", repoAbs, err)
		}
		ev, err := rules.NewCELEvaluator()
		if err != nil {
			return err
		}

		var cachePathAbs string
		if rulesCachePath != "" {
			if cachePathAbs, err = expandAndAbs(rulesCachePath); err != nil {
				return fmt.Errorf("resolving cache path: %w", err)
			}
		}
		res, status, err := rules.ResolveWithCache(rootAbs, ctx, ev, rules.CacheOptions{
			Path:         cachePathAbs,
			Disabled:     rulesNoCache,
			ForceRebuild: rulesRebuild,
		})
		if err != nil {
			return fmt.Errorf("resolving rules: %w", err)
		}

		out := cmd.OutOrStdout()
		if rulesShowManife {
			m := rules.NewManifest(res, rootAbs, repoAbs, time.Now().UTC()).WithCache(status)
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(m)
		}
		fmt.Fprintln(out, res.Bundle())
		return nil
	},
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

func init() {
	rulesResolveCmd.Flags().StringVar(&rulesRoot, "rules-root", "", "path to the rules tree to resolve against (required)")
	rulesResolveCmd.Flags().BoolVar(&rulesShowManife, "manifest", false, "print the JSON audit manifest instead of the assembled bundle")
	rulesResolveCmd.Flags().BoolVar(&rulesNoCache, "no-cache", false, "bypass the signals.yaml cache and resolve from the tree")
	rulesResolveCmd.Flags().BoolVar(&rulesRebuild, "rebuild-cache", false, "ignore any existing cache and regenerate it")
	rulesResolveCmd.Flags().StringVar(&rulesCachePath, "cache", "", "cache file path (default: <rules-root>/signals.yaml)")

	rulesBuildCmd.Flags().StringVar(&rulesBuildRoot, "rules-root", "", "path to the rules tree to index (required)")
	rulesBuildCmd.Flags().StringVarP(&rulesBuildOutput, "output", "o", "", "cache output path (default: <rules-root>/signals.yaml)")

	rulesCmd.AddCommand(rulesResolveCmd)
	rulesCmd.AddCommand(rulesBuildCmd)
	rootCmd.AddCommand(rulesCmd)
}
