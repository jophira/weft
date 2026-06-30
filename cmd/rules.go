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
--rules-root.

Examples:
  weft rules resolve . --rules-root ~/weft-sources/ai-rules-personal-tech/dev
  weft rules resolve /path/to/repo --rules-root ./dev --manifest`,
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
		res, err := rules.Resolve(rootAbs, ctx, ev)
		if err != nil {
			return fmt.Errorf("resolving rules: %w", err)
		}

		out := cmd.OutOrStdout()
		if rulesShowManife {
			m := rules.NewManifest(res, rootAbs, repoAbs, time.Now().UTC())
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(m)
		}
		fmt.Fprintln(out, res.Bundle())
		return nil
	},
}

func init() {
	rulesResolveCmd.Flags().StringVar(&rulesRoot, "rules-root", "", "path to the rules tree to resolve against (required)")
	rulesResolveCmd.Flags().BoolVar(&rulesShowManife, "manifest", false, "print the JSON audit manifest instead of the assembled bundle")
	rulesCmd.AddCommand(rulesResolveCmd)
	rootCmd.AddCommand(rulesCmd)
}
