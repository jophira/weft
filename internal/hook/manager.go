package hook

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/yamlstore"
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// FileManager persists each Hook as a YAML file under a directory.
type FileManager struct {
	store *yamlstore.Store[Hook]
}

func NewFileManager(dir string) *FileManager {
	return &FileManager{store: yamlstore.New[Hook](locate.ExpandHome(dir))}
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
	if m.store.Exists(h.Name) {
		return fmt.Errorf("hook %q already exists — remove it first with 'weft hook remove %s'", h.Name, h.Name)
	}
	return m.store.Write(h.Name, h)
}

// Remove deletes the hook YAML file.
func (m *FileManager) Remove(name string) error {
	if err := m.store.Remove(name); err != nil {
		if errors.Is(err, yamlstore.ErrNotFound) {
			return fmt.Errorf("hook %q not found", name)
		}
		return fmt.Errorf("removing hook %q: %w", name, err)
	}
	return nil
}

// Get reads and parses one hook by name.
func (m *FileManager) Get(name string) (*Hook, error) {
	h, err := m.store.Get(name)
	if err != nil {
		if errors.Is(err, yamlstore.ErrNotFound) {
			return nil, fmt.Errorf("hook %q not found", name)
		}
		return nil, fmt.Errorf("reading hook %q: %w", name, err)
	}
	return h, nil
}

// List returns all registered hooks sorted by filename.
func (m *FileManager) List() ([]Hook, error) {
	return m.store.List()
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
		// Reject absolute paths on any OS. filepath.IsAbs only recognises the
		// host OS's form, so a leading "/" or "\" (rooted on the other OS, e.g. a
		// Unix path in a config synced to Windows) is rejected explicitly too.
		if filepath.IsAbs(h.Action.SummaryTo) ||
			strings.HasPrefix(h.Action.SummaryTo, "/") ||
			strings.HasPrefix(h.Action.SummaryTo, `\`) {
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
