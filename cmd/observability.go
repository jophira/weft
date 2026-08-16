package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/advice"
	"github.com/jophira/weft/internal/logger"
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
}

// setObservabilityDefaults registers the viper defaults for logging and advice.
// Called from initConfig so a config file or env var still overrides them.
func setObservabilityDefaults() {
	viper.SetDefault("log_level", "info")
	viper.SetDefault("log_max_kb", int(logger.DefaultMaxLogBytes/1024))
	viper.SetDefault("log_generations", logger.DefaultGenerations)
	viper.SetDefault("advice_throttle_hours", int(advice.DefaultThrottle/time.Hour))
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
