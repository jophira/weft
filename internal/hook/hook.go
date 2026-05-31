package hook

// Trigger defines when a hook fires.
type Trigger string

const (
	TriggerSessionEnd Trigger = "session_end"
	TriggerManual     Trigger = "manual"
	TriggerFileChange Trigger = "file_change"
	TriggerPostCommit Trigger = "git_post_commit"
)

// ActionType defines what a hook does when fired.
type ActionType string

const (
	ActionAIImprove    ActionType = "ai_improve"
	ActionAppendMemory ActionType = "append_memory"
	ActionShell        ActionType = "shell"
)

// Action is the effect of a hook firing.
type Action struct {
	Type           ActionType `yaml:"type"            mapstructure:"type"`
	TargetSource   string     `yaml:"target_source"   mapstructure:"target_source"`
	RequireConfirm bool       `yaml:"require_confirm" mapstructure:"require_confirm"`
	SummaryTo      string     `yaml:"summary_to"      mapstructure:"summary_to"`
	Command        string     `yaml:"command"         mapstructure:"command"`
	Prompt         string     `yaml:"prompt"          mapstructure:"prompt"`
}

// Hook pairs a trigger with an action and an optional AI prompt.
type Hook struct {
	Name    string  `yaml:"name"    mapstructure:"name"`
	Trigger Trigger `yaml:"trigger" mapstructure:"trigger"`
	Prompt  string  `yaml:"prompt"  mapstructure:"prompt"`
	Action  Action  `yaml:"action"  mapstructure:"action"`
}

// Runner executes hooks.
type Runner interface {
	Run(h Hook) error
	List() ([]Hook, error)
}
