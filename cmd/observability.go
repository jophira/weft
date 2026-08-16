package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/advice"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/logger"
	"github.com/jophira/weft/internal/project"
)

// logLevelFlag backs --log-level. Empty means "not set", so the config file and
// WEFT_LOG_LEVEL still get a say.
var logLevelFlag string

// runID identifies this invocation across every log line it produces. Exposed to
// the rest of cmd/ so a subsystem logging outside the default logger can still
// correlate.
var runID string

// envLogLevel is read directly rather than through viper's AutomaticEnv, which
// would bind the bare name LOG_LEVEL and collide with unrelated tooling.
const envLogLevel = "WEFT_LOG_LEVEL"

// initObservability starts the logger and the advice bus for this run.
//
// It runs in PersistentPreRun, which cobra calls after the OnInitialize hooks,
// so viper has already read config.yaml and the file's values are available
// here. Everything degrades to a default rather than failing: a CLI that will
// not start because a log level was misspelt is worse than one that logs at Info.
func initObservability(cmd *cobra.Command) {
	runID = logger.NewRunID()

	level, ok := resolveLogLevel()
	logger.Init(Version, logger.Options{
		Level:       level,
		MaxBytes:    int64(viper.GetInt("log_max_kb")) * 1024,
		Generations: viper.GetInt("log_generations"),
		RunID:       runID,
	})
	if !ok {
		// Reported after Init so the complaint itself is logged, and to stderr so
		// it cannot land in a stdout contract.
		fmt.Fprintf(os.Stderr, "[weft] unrecognised log level %q, using info\n", rawLogLevel())
		slog.Warn("unrecognised log level", slog.String("value", rawLogLevel()))
	}

	// Named log_level, not level: slog's JSON handler already emits "level" for
	// the record's own severity, and a second key of that name produces a line
	// with duplicate keys that parsers resolve inconsistently.
	slog.Info("run",
		slog.String("cmd", cmd.CommandPath()),
		slog.String("log_level", level.String()),
	)

	advice.SetDefault(advice.New(advice.Options{
		Muted:    viper.GetStringSlice("advice_muted"),
		Throttle: time.Duration(viper.GetInt("advice_throttle_hours")) * time.Hour,
		Store:    adviceStore(),
	}))

	// After SetDefault, not before: this raises a hint, and adding it to the bus
	// that is about to be replaced would drop it silently.
	applyHarnessHome()
}

// envHarnessHome redirects every harness path, so an apply can be exercised
// without touching the real ~/.claude, ~/.codex and the rest.
const envHarnessHome = "WEFT_HARNESS_HOME"

// applyHarnessHome installs the harness home override, if one is configured.
//
// Env first, then config, so a one-off test run needs no file edit. Nothing is
// overridden by default: applying to the real harnesses is what weft is for.
//
// --config does not imply this. It reads as though it should, which is exactly
// how #265 came about, but implying it would silently change where an existing
// user's applies land. The hint below closes that gap by saying plainly what
// --config does and does not cover.
func applyHarnessHome() {
	home := os.Getenv(envHarnessHome)
	if home == "" {
		home = viper.GetString("harness_home")
	}
	if home == "" {
		if cfgFile != "" {
			advice.Add(advice.Advice{
				Code:     advice.CodeConfigHomeNotIsolated,
				Severity: advice.Info,
				Message:  "--config isolates weft's own state, but applies still write to the real home harness directories",
				Fix:      "set " + envHarnessHome + "=<dir> (or harness_home in config) to redirect them too",
			})
		}
		return
	}
	expanded := locate.ExpandHome(home)
	if abs, err := filepath.Abs(expanded); err == nil {
		expanded = abs
	}
	locate.SetHarnessHome(expanded)
	slog.Info("harness_home", slog.String("path", expanded))
}

// setObservabilityDefaults registers the viper defaults for logging and advice.
// Called from initConfig so a config file or env var still overrides them.
func setObservabilityDefaults() {
	viper.SetDefault("log_level", "info")
	viper.SetDefault("log_max_kb", int(logger.DefaultMaxLogBytes/1024))
	viper.SetDefault("log_generations", logger.DefaultGenerations)
	viper.SetDefault("advice_throttle_hours", int(advice.DefaultThrottle/time.Hour))
	viper.SetDefault("project_sync", "on-visit")
	viper.SetDefault("project_max_age_days", int(project.DefaultMaxAge/(24*time.Hour)))
}

// rawLogLevel returns the level as the user spelled it, flag first, then env,
// then config. Used only for the "unrecognised" message.
func rawLogLevel() string {
	if logLevelFlag != "" {
		return logLevelFlag
	}
	if v := os.Getenv(envLogLevel); v != "" {
		return v
	}
	return viper.GetString("log_level")
}

// resolveLogLevel applies flag over env over config, and reports whether the
// value was recognised. An unset level is recognised and means Info.
func resolveLogLevel() (slog.Level, bool) {
	return logger.ParseLevel(rawLogLevel())
}

// adviceStore returns the throttle store, or nil when the config dir cannot be
// resolved. A nil store throttles nothing, which is the right failure mode: the
// user sees a repeated hint rather than losing it entirely.
func adviceStore() advice.Store {
	dir := configDir()
	if dir == "" {
		return nil
	}
	return advice.NewFileStore(dir)
}
