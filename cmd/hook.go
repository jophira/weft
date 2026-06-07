package cmd

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/hook"
)

// newHookManager builds a FileManager using the configured hooks directory.
func newHookManager() *hook.FileManager {
	dir := viper.GetString("hooks_dir")
	if dir == "" {
		cfg, _ := config.Defaults()
		dir = cfg.HooksDir
	}
	return hook.NewFileManager(dir)
}

// newExecutor builds an Executor using the configured sources directory.
func newExecutor() *hook.Executor {
	dir := viper.GetString("sources_dir")
	if dir == "" {
		cfg, _ := config.Defaults()
		dir = cfg.SourcesDir
	}
	return hook.NewExecutor(dir)
}

// ── Flags ─────────────────────────────────────────────────────────────────────

var (
	hookTrigger   string
	hookAction    string
	hookCommand   string
	hookSource    string
	hookSummaryTo string
	hookPrompt    string
	hookConfirm   bool
	hookFile      string
)

// ── Commands ──────────────────────────────────────────────────────────────────

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Manage lifecycle hooks",
}

var hookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered hooks",
	RunE: func(cmd *cobra.Command, args []string) error {
		hooks, err := newHookManager().List()
		if err != nil {
			return err
		}
		if len(hooks) == 0 {
			fmt.Println("No hooks registered.")
			fmt.Println("Add one with: weft hook add <name> --trigger manual --action shell --command <cmd>")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tTRIGGER\tACTION\tCONFIRM\tDETAIL")
		for _, h := range hooks {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				h.Name,
				h.Trigger,
				h.Action.Type,
				boolWord(h.Action.RequireConfirm),
				hookDetail(h),
			)
		}
		return w.Flush()
	},
}

// hookDetail returns a short one-liner describing what the action does.
func hookDetail(h hook.Hook) string {
	switch h.Action.Type {
	case hook.ActionShell:
		return h.Action.Command
	case hook.ActionAppendMemory:
		return fmt.Sprintf("%s → %s", h.Action.TargetSource, h.Action.SummaryTo)
	case hook.ActionAIImprove:
		return fmt.Sprintf("source:%s file:%s", h.Action.TargetSource, h.Action.SummaryTo)
	default:
		return "-"
	}
}

var hookRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Manually execute a hook (ignores trigger type)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr := newHookManager()
		h, err := mgr.Get(args[0])
		if err != nil {
			return err
		}

		exec := newExecutor()
		fmt.Printf("Running hook %q (%s/%s)...\n", h.Name, h.Trigger, h.Action.Type)
		err = exec.Run(*h)
		if errors.Is(err, hook.ErrConfirmRequired) {
			if !confirm(fmt.Sprintf("Run hook %q? [y/N] ", h.Name)) {
				fmt.Println("Aborted.")
				return nil
			}
			err = exec.RunConfirmed(*h)
		}
		if err != nil {
			return fmt.Errorf("hook failed: %w", err)
		}
		fmt.Printf("✓ Done\n")
		return nil
	},
}

var hookAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Register a new hook",
	Long: `Register a lifecycle hook.

Use --file to load a complete hook definition from a YAML file:

  weft hook add my-hook --file ./my-hook.yaml

Or specify the hook inline with flags:

  weft hook add post-sync --trigger manual --action shell --command "weft source sync"
  weft hook add log-session --trigger manual --action append_memory \
      --source personal --summary-to memory/LEARNINGS.md \
      --prompt "Session summary goes here"

Trigger types:  manual  session_end  file_change  git_post_commit
Action types:   shell   append_memory   ai_improve`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		var h hook.Hook

		if hookFile != "" {
			data, err := os.ReadFile(hookFile)
			if err != nil {
				return fmt.Errorf("reading hook file: %w", err)
			}
			if err := yaml.Unmarshal(data, &h); err != nil {
				return fmt.Errorf("parsing hook file: %w", err)
			}
			// Positional arg always sets the name, so the file can omit it.
			h.Name = name
		} else {
			if hookTrigger == "" {
				return fmt.Errorf("--trigger is required (or use --file to load from YAML)")
			}
			if hookAction == "" {
				return fmt.Errorf("--action is required (or use --file to load from YAML)")
			}
			h = hook.Hook{
				Name:    name,
				Trigger: hook.Trigger(hookTrigger),
				Prompt:  hookPrompt,
				Action: hook.Action{
					Type:           hook.ActionType(hookAction),
					Command:        hookCommand,
					TargetSource:   hookSource,
					SummaryTo:      hookSummaryTo,
					RequireConfirm: hookConfirm,
				},
			}
		}

		if err := newHookManager().Add(h); err != nil {
			return err
		}

		fmt.Printf("✓ Hook %q registered\n", h.Name)
		fmt.Printf("  trigger: %s\n", h.Trigger)
		fmt.Printf("  action:  %s\n", h.Action.Type)
		switch h.Action.Type {
		case hook.ActionShell:
			fmt.Printf("  command: %s\n", h.Action.Command)
		case hook.ActionAppendMemory:
			fmt.Printf("  source:  %s\n", h.Action.TargetSource)
			fmt.Printf("  file:    %s\n", h.Action.SummaryTo)
		}
		if h.Action.RequireConfirm {
			fmt.Printf("  confirm: yes\n")
		}
		fmt.Printf("\nRun manually with: weft hook run %s\n", h.Name)
		return nil
	},
}

var hookRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Deregister a hook",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := newHookManager().Remove(args[0]); err != nil {
			return err
		}
		fmt.Printf("✓ Hook %q removed\n", args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(hookCmd)
	hookCmd.AddCommand(hookListCmd, hookRunCmd, hookAddCmd, hookRemoveCmd)

	hookAddCmd.Flags().StringVar(&hookTrigger, "trigger", "", "when the hook fires: manual|session_end|file_change|git_post_commit")
	hookAddCmd.Flags().StringVar(&hookAction, "action", "", "what the hook does: shell|append_memory|ai_improve")
	hookAddCmd.Flags().StringVar(&hookCommand, "command", "", "shell command to run (shell action)")
	hookAddCmd.Flags().StringVar(&hookSource, "source", "", "target source name (append_memory action)")
	hookAddCmd.Flags().StringVar(&hookSummaryTo, "summary-to", "", "file path within source root (append_memory action)")
	hookAddCmd.Flags().StringVar(&hookPrompt, "prompt", "", "text to append or AI prompt")
	hookAddCmd.Flags().BoolVar(&hookConfirm, "confirm", false, "ask for confirmation before running")
	hookAddCmd.Flags().StringVar(&hookFile, "file", "", "load hook definition from a YAML file")
}
