package cmd

import (
	"log/slog"
	"os"
	"time"

	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/project"
)

// projectSyncOff is the config value that disables project tracking entirely.
const projectSyncOff = "off"

// registerWriteInterval is how stale an entry's timestamp must be before a
// visit that changed nothing still rewrites the registry.
//
// `weft status --short` runs in a status line, which can fire every turn. Saving
// on every visit would rewrite the file constantly for no new information, so an
// unchanged visit only refreshes the timestamp on disk once an hour.
const registerWriteInterval = time.Hour

// registerCurrentProject records the repository the command is running in.
//
// Called from PersistentPreRun rather than from `weft rules resolve` alone, so
// registration covers the session hook, a status line, and every ad-hoc command
// typed in the repository. Nothing is ever registered on purpose, because a step
// the user has to remember is a step that will be forgotten.
//
// Every failure is silent. Registration is bookkeeping for a feature the user
// did not invoke; failing their actual command over it would be absurd.
func registerCurrentProject() {
	if viper.GetString("project_sync") == projectSyncOff {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	root, ok := project.FindRoot(cwd)
	if !ok {
		return // not in a repository; nothing to track
	}
	cfgDir := configDir()
	if cfgDir == "" {
		return
	}
	reg, err := project.Load(cfgDir)
	if err != nil {
		slog.Warn("project.register", slog.String("stage", "load"), slog.String("error", err.Error()))
		return
	}

	now := time.Now().UTC()
	prev := reg.Get(root)
	repo, remote := project.Identity(root)

	changed := reg.Upsert(project.Project{
		Root:     root,
		Repo:     repo,
		Remote:   remote,
		Profile:  activeProfileName(),
		LastSeen: now,
		Enabled:  true,
	})
	// Pruning rides along with registration so the registry never needs manual
	// gardening: dead roots and long-unvisited entries go on the next visit
	// anywhere.
	dropped := reg.Prune(now, projectMaxAge(), project.DirExists)

	if !changed && len(dropped) == 0 && !timestampDue(prev, now) {
		return
	}
	if err := project.Save(cfgDir, reg); err != nil {
		slog.Warn("project.register", slog.String("stage", "save"), slog.String("error", err.Error()))
		return
	}
	slog.Info("project.register",
		slog.String("root", root),
		slog.String("repo", repo),
		slog.Bool("new", prev == nil),
		slog.Int("pruned", len(dropped)),
	)
	for _, d := range dropped {
		slog.Info("project.pruned", slog.String("root", d.Root), slog.String("repo", d.Repo))
	}
}

// timestampDue reports whether an otherwise unchanged visit should still be
// written, so LastSeen does not drift far enough behind to look stale.
func timestampDue(prev *project.Project, now time.Time) bool {
	if prev == nil {
		return true
	}
	return now.Sub(prev.LastSeen) > registerWriteInterval
}

// projectMaxAge returns the staleness window from config.
func projectMaxAge() time.Duration {
	days := viper.GetInt("project_max_age_days")
	if days <= 0 {
		return project.DefaultMaxAge
	}
	return time.Duration(days) * 24 * time.Hour
}
