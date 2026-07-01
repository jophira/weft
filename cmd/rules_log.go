package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/rules"
)

var (
	rulesLogGlobal bool
	rulesLogFile   string
	rulesLogJSON   bool
	rulesLogLimit  int
	rulesLogLabel  string
	rulesLogMonth  string
)

// shortHashLen is how many leading hex characters of a resolution hash the human
// report shows — enough to disambiguate selections at a glance.
const shortHashLen = 8

var rulesLogCmd = &cobra.Command{
	Use:   "log [repo-path]",
	Short: "Report the recorded resolve history",
	Long: `Read the deduped JSONL audit logs written by "weft rules resolve --record"
and print the resolve history — one line per distinct selection change.

By default it reads the current repository's .weft/resolve.log.jsonl. Use
--global to merge the machine-wide monthly rollups under ~/.weft/audit, or
--file to point at a specific log. The read is total: a missing log is simply an
empty history, and corrupt lines are skipped.

Examples:
  weft rules log
  weft rules log --limit 5
  weft rules log --label springboot
  weft rules log --global --month 2026-07
  weft rules log --json | jq .`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := rulesLogPaths(args)
		if err != nil {
			return err
		}

		records, err := loadRecords(paths)
		if err != nil {
			return err
		}
		records = filterRecords(records, rulesLogLabel)
		sort.SliceStable(records, func(i, j int) bool {
			return records[i].Timestamp.Before(records[j].Timestamp)
		})
		records = lastN(records, rulesLogLimit)

		out := cmd.OutOrStdout()
		if rulesLogJSON {
			return writeRecordsJSON(out, records)
		}
		return writeRecordsTable(out, records)
	},
}

// rulesLogPaths determines which log file(s) to read from the flags and an
// optional repo-path argument.
func rulesLogPaths(args []string) ([]string, error) {
	if rulesLogFile != "" {
		abs, err := expandAndAbs(rulesLogFile)
		if err != nil {
			return nil, fmt.Errorf("resolving --file: %w", err)
		}
		return []string{abs}, nil
	}

	if rulesLogGlobal {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("locating home directory: %w", err)
		}
		auditDir := filepath.Join(home, ".weft", "audit")
		if rulesLogMonth != "" {
			return []string{filepath.Join(auditDir, rulesLogMonth+".jsonl")}, nil
		}
		matches, err := filepath.Glob(filepath.Join(auditDir, "*.jsonl"))
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", auditDir, err)
		}
		sort.Strings(matches) // YYYY-MM names sort chronologically
		return matches, nil
	}

	repo := "."
	if len(args) == 1 {
		repo = args[0]
	}
	repoAbs, err := expandAndAbs(repo)
	if err != nil {
		return nil, fmt.Errorf("resolving repo path: %w", err)
	}
	return []string{filepath.Join(repoAbs, ".weft", "resolve.log.jsonl")}, nil
}

// loadRecords reads and concatenates every path's records. Missing files
// contribute nothing (they are not an error); a genuine read error surfaces.
func loadRecords(paths []string) ([]rules.ResolveRecord, error) {
	var all []rules.ResolveRecord
	for _, p := range paths {
		recs, err := rules.ReadRecords(p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		all = append(all, recs...)
	}
	return all, nil
}

// filterRecords keeps only records with a loaded label containing labelSubstr
// (case-insensitive). An empty filter keeps everything.
func filterRecords(records []rules.ResolveRecord, labelSubstr string) []rules.ResolveRecord {
	if labelSubstr == "" {
		return records
	}
	needle := strings.ToLower(labelSubstr)
	out := make([]rules.ResolveRecord, 0, len(records))
	for _, r := range records {
		for _, label := range r.Labels() {
			if strings.Contains(strings.ToLower(label), needle) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// lastN returns the final n records (the most recent after chronological sort).
// n <= 0 returns all of them.
func lastN(records []rules.ResolveRecord, n int) []rules.ResolveRecord {
	if n <= 0 || n >= len(records) {
		return records
	}
	return records[len(records)-n:]
}

// writeRecordsJSON emits one compact JSON object per line, suitable for piping.
func writeRecordsJSON(out io.Writer, records []rules.ResolveRecord) error {
	enc := json.NewEncoder(out)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

// writeRecordsTable renders the human report: aligned time / profile / short
// hash / labels, with a count footer.
func writeRecordsTable(out io.Writer, records []rules.ResolveRecord) error {
	if len(records) == 0 {
		fmt.Fprintln(out, "no resolve history")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tPROFILE\tHASH\tLABELS")
	for _, r := range records {
		profile := r.Profile
		if profile == "" {
			profile = "-"
		}
		labels := strings.Join(r.Labels(), ", ")
		if labels == "" {
			labels = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			r.Timestamp.UTC().Format("2006-01-02T15:04:05Z"), profile, shortHash(r.ResolutionHash), labels)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d record(s)\n", len(records))
	return nil
}

// shortHash truncates a resolution hash for display.
func shortHash(h string) string {
	if len(h) <= shortHashLen {
		return h
	}
	return h[:shortHashLen]
}

func init() {
	rulesLogCmd.Flags().BoolVar(&rulesLogGlobal, "global", false, "read the machine-wide monthly rollups under ~/.weft/audit instead of the repo log")
	rulesLogCmd.Flags().StringVar(&rulesLogFile, "file", "", "read a specific JSONL log file (overrides repo/--global)")
	rulesLogCmd.Flags().BoolVar(&rulesLogJSON, "json", false, "emit records as JSONL instead of a table")
	rulesLogCmd.Flags().IntVar(&rulesLogLimit, "limit", 20, "show the last N records (0 = all)")
	rulesLogCmd.Flags().StringVar(&rulesLogLabel, "label", "", "only records whose loaded labels include this substring")
	rulesLogCmd.Flags().StringVar(&rulesLogMonth, "month", "", "with --global, restrict to one YYYY-MM rollup")

	rulesCmd.AddCommand(rulesLogCmd)
}
