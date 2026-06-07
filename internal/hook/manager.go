package hook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jophira/weft/internal/locate"
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// FileManager persists each Hook as a YAML file under a directory.
type FileManager struct {
	dir string // absolute path to ~/.config/weft/hooks/
}

func NewFileManager(dir string) *FileManager {
	return &FileManager{dir: locate.ExpandHome(dir)}
}

// Add writes a new hook YAML file. Errors if the name already exists.
func (m *FileManager) Add(h Hook) error {
	if !validName.MatchString(h.Name) {
		return fmt.Errorf(
			"invalid name %q: must start with a letter and contain only lowercase letters, digits, hyphens or underscores",
			h.Name,
		)
	}
	if err := validateHook(h); err != nil {
		return err
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("creating hooks directory: %w", err)
	}
	p := m.filePath(h.Name)
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("hook %q already exists — remove it first with 'weft hook remove %s'", h.Name, h.Name)
	}
	data, err := yaml.Marshal(&h)
	if err != nil {
		return fmt.Errorf("serialising hook: %w", err)
	}
	return os.WriteFile(p, data, 0o644)
}

// Remove deletes the hook YAML file.
func (m *FileManager) Remove(name string) error {
	if err := os.Remove(m.filePath(name)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("hook %q not found", name)
		}
		return fmt.Errorf("removing hook %q: %w", name, err)
	}
	return nil
}

// Get reads and parses one hook by name.
func (m *FileManager) Get(name string) (*Hook, error) {
	data, err := os.ReadFile(m.filePath(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("hook %q not found", name)
		}
		return nil, fmt.Errorf("reading hook %q: %w", name, err)
	}
	var h Hook
	if err := yaml.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("parsing hook %q: %w", name, err)
	}
	return &h, nil
}

// List returns all registered hooks sorted by filename.
func (m *FileManager) List() ([]Hook, error) {
	entries, err := os.ReadDir(m.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading hooks directory: %w", err)
	}
	var hooks []Hook
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		h, err := m.Get(name)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, *h)
	}
	return hooks, nil
}

func (m *FileManager) filePath(name string) string {
	return filepath.Join(m.dir, name+".yaml")
}

// validateHook checks that the trigger/action combination is coherent.
func validateHook(h Hook) error {
	if !isValidTrigger(h.Trigger) {
		return fmt.Errorf("invalid trigger %q: must be one of manual, session_end, file_change, git_post_commit", h.Trigger)
	}
	if !isValidAction(h.Action.Type) {
		return fmt.Errorf("invalid action %q: must be one of shell, append_memory, ai_improve", h.Action.Type)
	}
	switch h.Action.Type {
	case ActionShell:
		if h.Action.Command == "" {
			return fmt.Errorf("shell action requires --command")
		}
	case ActionAppendMemory:
		if h.Action.TargetSource == "" {
			return fmt.Errorf("append_memory action requires --source")
		}
		if h.Action.SummaryTo == "" {
			return fmt.Errorf("append_memory action requires --summary-to")
		}
		if filepath.IsAbs(h.Action.SummaryTo) {
			return fmt.Errorf("summary_to must be a relative path")
		}
		// Reject .. components as an early defence-in-depth check.
		if strings.HasPrefix(filepath.Clean(h.Action.SummaryTo), "..") {
			return fmt.Errorf("summary_to must not escape the source root")
		}
	}
	return nil
}

func isValidTrigger(t Trigger) bool {
	switch t {
	case TriggerManual, TriggerSessionEnd, TriggerFileChange, TriggerPostCommit:
		return true
	}
	return false
}

func isValidAction(a ActionType) bool {
	switch a {
	case ActionShell, ActionAppendMemory, ActionAIImprove:
		return true
	}
	return false
}
