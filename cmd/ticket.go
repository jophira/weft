package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// ticketArtifacts are the files scaffolded for a new ticket, each backed by a
// template of the same base name under ~/weft/templates (with a built-in
// fallback). The main ticket file is named after the ticket id.
var ticketArtifacts = []struct {
	template string // template basename under ~/weft/templates
	outName  string // output filename; "" means "<TICKET>.md"
	fallback string // built-in template used when the file is absent
}{
	{"ticket.md", "", defaultTicketTemplate},
	{"estimate.md", "estimate.md", defaultEstimateTemplate},
	{"analysis.md", "analysis.md", defaultAnalysisTemplate},
	{"plan.md", "plan.md", defaultPlanTemplate},
}

var ticketCmd = &cobra.Command{
	Use:   "ticket",
	Short: "Manage work-plane tickets under ~/weft/work/tickets",
}

var ticketNewCmd = &cobra.Command{
	Use:   "new <TICKET-ID>",
	Short: "Scaffold a ticket folder from templates",
	Long: `Create ~/weft/work/tickets/<TICKET-ID>/ with the ticket, estimate,
analysis and plan files, filled from ~/weft/templates (or built-in defaults).
The {{ticket}} placeholder in a template is replaced with the ticket id.

Idempotent: existing files are left untouched, so re-running only adds what is
missing.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := strings.TrimSpace(args[0])
		if id == "" || strings.ContainsAny(id, `/\`) {
			return fmt.Errorf("invalid ticket id %q", args[0])
		}
		home := weftHomeDir()
		if home == "" {
			return fmt.Errorf("cannot resolve weft home directory")
		}
		dir := filepath.Join(home, "work", "tickets", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating ticket dir: %w", err)
		}
		templatesDir := filepath.Join(home, "templates")
		out := cmd.OutOrStdout()

		created := 0
		for _, a := range ticketArtifacts {
			name := a.outName
			if name == "" {
				name = id + ".md"
			}
			dst := filepath.Join(dir, name)
			if fileExists(dst) {
				fmt.Fprintf(out, "  exists   %s\n", dst)
				continue
			}
			body := loadTemplate(filepath.Join(templatesDir, a.template), a.fallback)
			body = strings.ReplaceAll(body, "{{ticket}}", id)
			if err := os.WriteFile(dst, []byte(body), 0o644); err != nil { //nolint:gosec // weft-owned work plane
				return fmt.Errorf("writing %s: %w", dst, err)
			}
			fmt.Fprintf(out, "  created  %s\n", dst)
			created++
		}
		if created == 0 {
			fmt.Fprintf(out, "ticket %s already scaffolded.\n", id)
		} else {
			fmt.Fprintf(out, "ticket %s ready at %s\n", id, dir)
		}
		return nil
	},
}

var ticketListCmd = &cobra.Command{
	Use:   "list",
	Short: "List scaffolded tickets",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		home := weftHomeDir()
		if home == "" {
			return fmt.Errorf("cannot resolve weft home directory")
		}
		ticketsDir := filepath.Join(home, "work", "tickets")
		entries, err := os.ReadDir(ticketsDir)
		if os.IsNotExist(err) {
			fmt.Fprintln(cmd.OutOrStdout(), "no tickets yet — create one with 'weft ticket new <ID>'")
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", ticketsDir, err)
		}
		var ids []string
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "plan" {
				ids = append(ids, e.Name())
			}
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no tickets yet — create one with 'weft ticket new <ID>'")
			return nil
		}
		for _, id := range ids {
			fmt.Fprintln(cmd.OutOrStdout(), id)
		}
		return nil
	},
}

// loadTemplate returns the contents of path, or fallback when path is absent or
// unreadable.
func loadTemplate(path, fallback string) string {
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	return fallback
}

func init() {
	ticketCmd.AddCommand(ticketNewCmd, ticketListCmd)
	rootCmd.AddCommand(ticketCmd)
}
