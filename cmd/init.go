package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// weftLayout is the resolved ADR-0003 directory layout for this machine: the
// consumer-facing workbench (~/weft) plus the engine-room state (~/.config/weft).
// Paths honour --config isolation and any *_dir overrides.
type weftLayout struct {
	Home      string // ~/weft — workbench root
	ConfigDir string // ~/.config/weft — engine room
	Sources   string // registry (engine-room *.yaml pointer files)
	Profiles  string // profile definitions (engine-room)
	Hooks     string
	Audit     string
	Docs      string
	// ContentSources is the workbench content home ~/weft/sources, where source
	// repos land via 'weft source relocate'. Distinct from Sources (the registry).
	ContentSources string
	Templates      string
	Work           string
	WorkProjects   string
	WorkTickets    string
	WorkPlans      string
	WorkInbox      string
}

// resolveLayout reads the effective layout from viper. Sources/Profiles are the
// engine-room bookkeeping dirs; ContentSources/Templates/Work sit under the
// workbench. Source content is relocated into ContentSources by
// 'weft source relocate', not scaffolded with content here.
func resolveLayout() weftLayout {
	home := weftHomeDir()
	work := filepath.Join(home, "work")
	return weftLayout{
		Home:           home,
		ConfigDir:      configDir(),
		Sources:        viper.GetString("sources_dir"),
		Profiles:       viper.GetString("profiles_dir"),
		Hooks:          viper.GetString("hooks_dir"),
		Audit:          auditDir(),
		Docs:           docsDir(),
		ContentSources: filepath.Join(home, "sources"),
		Templates:      filepath.Join(home, "templates"),
		Work:           work,
		WorkProjects:   filepath.Join(work, "projects"),
		WorkTickets:    filepath.Join(work, "tickets"),
		WorkPlans:      filepath.Join(work, "plans"),
		WorkInbox:      filepath.Join(work, "inbox"),
	}
}

const weftHomeReadme = `# weft workbench

This is your weft home (ADR 0003) — the consumer-facing root for content you
author, edit, and share. Engine-room state weft regenerates lives separately
under ~/.config/weft.

    sources/     source repos weft layers into your harnesses (via 'weft source relocate')
    templates/   ticket / adr / estimate skeletons
    docs/        project docs (present only after 'weft docs adopt')
    work/        what you produce while working
      projects/  per-repo knowledge base + notes
      tickets/   <TICKET>/<TICKET>.md + estimate / analysis / plan
      plans/     planning with no ticket yet
      inbox/     quick capture before triage

Re-running 'weft init' only adds what is missing — it never overwrites your work.
`

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold the weft home directories (idempotent)",
	Long: `Create the weft workbench (~/weft) and engine-room (~/.config/weft)
directories if they are absent. Safe to re-run: init only creates missing
directories and never overwrites authored content, so you can run it again after
upgrading to pick up new scaffolding.

Directories created:
  ~/weft/{sources,profiles,templates}
  ~/weft/work/{projects,tickets,plans,inbox}
  ~/.config/weft/{hooks,audit}

Sources/profiles are created at their currently-resolved location, so a
pre-migration layout is left untouched — run 'weft migrate' to relocate it.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		l := resolveLayout()
		out := cmd.OutOrStdout()

		dirs := []struct{ label, path string }{
			// Workbench (~/weft): consumer-facing content.
			{"sources (content)", l.ContentSources},
			{"templates", l.Templates},
			{"work/projects", l.WorkProjects},
			{"work/tickets", l.WorkTickets},
			{"work/plans", l.WorkPlans},
			{"work/inbox", l.WorkInbox},
			// Engine room (~/.config/weft): bookkeeping.
			{"registry", l.Sources},
			{"profiles", l.Profiles},
			{"hooks", l.Hooks},
			{"audit", l.Audit},
		}
		created := 0
		for _, d := range dirs {
			if d.path == "" {
				continue
			}
			existed := dirExists(d.path)
			if err := os.MkdirAll(d.path, 0o755); err != nil {
				return fmt.Errorf("creating %s (%s): %w", d.label, d.path, err)
			}
			if existed {
				fmt.Fprintf(out, "  exists   %s\n", d.path)
			} else {
				fmt.Fprintf(out, "  created  %s\n", d.path)
				created++
			}
		}

		// Seed work-plane templates, each written once and never overwritten so
		// edits survive re-running init.
		for name, body := range seedTemplates {
			tp := filepath.Join(l.Templates, name)
			if fileExists(tp) {
				continue
			}
			if err := os.WriteFile(tp, []byte(body), 0o644); err != nil { //nolint:gosec // weft-owned home
				return fmt.Errorf("writing template %s: %w", tp, err)
			}
			fmt.Fprintf(out, "  created  %s\n", tp)
			created++
		}

		// Home README: written once, never overwritten (idempotency).
		readme := filepath.Join(l.Home, "README.md")
		if l.Home != "" && !fileExists(readme) {
			if err := os.WriteFile(readme, []byte(weftHomeReadme), 0o644); err != nil { //nolint:gosec // weft-owned home
				return fmt.Errorf("writing %s: %w", readme, err)
			}
			fmt.Fprintf(out, "  created  %s\n", readme)
			created++
		}

		if initAdoptDocs {
			if err := adoptDocs(out, false); err != nil {
				return err
			}
		}

		if created == 0 {
			fmt.Fprintln(out, "weft home already scaffolded — nothing to do.")
		} else {
			fmt.Fprintf(out, "weft home ready (%d created).\n", created)
		}
		return nil
	},
}

var initAdoptDocs bool

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func init() {
	initCmd.Flags().BoolVar(&initAdoptDocs, "adopt-docs", false, "also consolidate ~/docs under ~/weft/docs (see 'weft docs adopt')")
	rootCmd.AddCommand(initCmd)
}
