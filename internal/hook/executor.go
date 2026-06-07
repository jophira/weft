package hook

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ErrConfirmRequired is returned when a hook has RequireConfirm set and the
// caller has not obtained user confirmation. The caller is responsible for
// prompting the user and re-invoking Run (or RunConfirmed) on approval.
var ErrConfirmRequired = errors.New("hook requires confirmation before running")

// Executor runs hook actions.
type Executor struct {
	sourcesDir string // absolute path to ~/.config/weft/sources/
}

func NewExecutor(sourcesDir string) *Executor {
	return &Executor{sourcesDir: expandHome(sourcesDir)}
}

// Run executes the action defined in h, regardless of trigger type.
// If h.Action.RequireConfirm is true and the action is a shell command,
// Run returns ErrConfirmRequired without executing anything. The caller must
// obtain confirmation and call RunConfirmed instead.
func (e *Executor) Run(h Hook) error {
	switch h.Action.Type {
	case ActionShell:
		if h.Action.RequireConfirm {
			return ErrConfirmRequired
		}
		return e.runShell(h)
	case ActionAppendMemory:
		return e.appendMemory(h)
	case ActionAIImprove:
		return fmt.Errorf("ai_improve is not yet implemented — use 'weft hook run' with a shell or append_memory action for now")
	default:
		return fmt.Errorf("unknown action type %q", h.Action.Type)
	}
}

// RunConfirmed executes the action without the RequireConfirm gate.
// Use this only after the caller has obtained explicit user confirmation.
func (e *Executor) RunConfirmed(h Hook) error {
	switch h.Action.Type {
	case ActionShell:
		return e.runShell(h)
	case ActionAppendMemory:
		return e.appendMemory(h)
	case ActionAIImprove:
		return fmt.Errorf("ai_improve is not yet implemented — use 'weft hook run' with a shell or append_memory action for now")
	default:
		return fmt.Errorf("unknown action type %q", h.Action.Type)
	}
}

// runShell executes the hook's command in a shell, inheriting stdio.
func (e *Executor) runShell(h Hook) error {
	cmd := exec.Command("sh", "-c", h.Action.Command) //nolint:gosec // intentional: runs user-defined hook commands
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed: %w", err)
	}
	return nil
}

// appendMemory appends the hook's prompt text (with a timestamp header) to the
// summary_to file inside the target source's root directory.
func (e *Executor) appendMemory(h Hook) error {
	content := strings.TrimSpace(h.Prompt)
	if content == "" {
		return fmt.Errorf("append_memory requires a non-empty prompt to append")
	}

	root, err := e.sourceRoot(h.Action.TargetSource)
	if err != nil {
		return err
	}

	target := filepath.Join(root, h.Action.SummaryTo)
	// Ensure target stays within root to prevent path traversal.
	rootWithSep := root + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(target)+string(filepath.Separator), rootWithSep) {
		return fmt.Errorf("summary_to %q escapes source root", h.Action.SummaryTo)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", h.Action.SummaryTo, err)
	}

	entry := fmt.Sprintf("\n## %s\n\n%s\n", time.Now().Format("2006-01-02 15:04:05"), content)

	f, err := os.OpenFile(target, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", target, err)
	}
	if _, err := f.WriteString(entry); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing to %s: %w", target, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", target, err)
	}

	fmt.Printf("✓ Appended to %s\n", contractHome(target))
	return nil
}

// sourceRoot reads the root path of the named source from its YAML file without
// importing the source package (avoids coupling at the package level).
func (e *Executor) sourceRoot(name string) (string, error) {
	type sourceYAML struct {
		Root string `yaml:"root"`
	}
	data, err := os.ReadFile(filepath.Join(e.sourcesDir, name+".yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("source %q not found — register it with 'weft source add'", name)
		}
		return "", fmt.Errorf("reading source %q: %w", name, err)
	}
	var s sourceYAML
	if err := yaml.Unmarshal(data, &s); err != nil {
		return "", fmt.Errorf("parsing source %q: %w", name, err)
	}
	if s.Root == "" {
		return "", fmt.Errorf("source %q has no root path", name)
	}
	return expandHome(s.Root), nil
}

func contractHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return "~/" + path[len(prefix):]
	}
	return path
}
