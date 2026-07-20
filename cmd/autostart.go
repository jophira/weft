package cmd

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/autostart"
	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/locate"
)

var (
	autostartProfile string
	autostartLinger  bool
	autostartRunWait time.Duration
)

// homeProbeInterval is how often `autostart run` re-checks for a home
// directory that has not mounted yet.
const homeProbeInterval = 500 * time.Millisecond

// defaultHomeWait bounds that wait. A network or encrypted $HOME usually
// mounts within seconds of logon; past a minute the right answer is to fail
// with a clear message rather than hang a service forever.
const defaultHomeWait = 60 * time.Second

var autostartCmd = &cobra.Command{
	Use:   "autostart",
	Short: "Run the weft watcher automatically at login",
	Long: `Install, remove, or inspect a per-user background service that keeps the weft
watcher running across reboots.

Autostart is strictly opt-in — no other weft command installs a service.

  Linux    systemd user unit      ~/.config/systemd/user/weft.service
  macOS    LaunchAgent            ~/Library/LaunchAgents/com.jophira.weft.plist
  Windows  Task Scheduler         logon task named "weft"

By default the service follows the active profile: it reads active_profile from
config.yaml at start, so the machine comes back up on whatever profile was last
switched to. Pass --profile to pin it to one profile instead.`,
}

var autostartEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Install and start the autostart service",
	Long: `Install the platform's autostart unit and start it immediately.

Re-running enable is how you re-point a stale unit after moving or upgrading
the weft binary — it overwrites the existing unit rather than failing.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := newAutostartService()
		if err != nil {
			return err
		}
		opts, err := autostartOptions()
		if err != nil {
			return err
		}
		if opts.Profile != "" {
			// Fail fast on a typo here rather than in a background service
			// whose only symptom is "weft is not running".
			if _, _, _, err := resolveProfileRoots(opts.Profile); err != nil {
				return err
			}
		}
		if err := svc.Enable(opts); err != nil {
			return err
		}

		st, err := svc.Status()
		if err != nil {
			return err
		}
		fmt.Printf("✓ Autostart enabled (%s)\n", st.Mechanism)
		fmt.Printf("  unit:    %s\n", locate.Tilde(st.UnitPath))
		fmt.Printf("  binary:  %s\n", locate.Tilde(opts.BinPath))
		fmt.Printf("  profile: %s\n", autostartProfileLabel(opts.Profile))
		if autostartLinger {
			// Linger is a systemd concept. Off Linux, say the flag was ignored
			// rather than letting the user believe it took effect.
			if lingerHint == "" {
				fmt.Println("  linger:  ignored — this platform has no linger equivalent")
			} else {
				fmt.Println("  linger:  enabled (watcher runs without an active login session)")
			}
		}

		// Trailing advisories, after the field block.
		if opts.Profile == "" && activeProfileName() == "" {
			fmt.Println("\nNo active profile yet — the service will idle until you run 'weft profile use <name>'.")
		}
		if !autostartLinger && lingerHint != "" {
			fmt.Printf("\n%s\n", lingerHint)
		}
		return nil
	},
}

var autostartDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Stop and remove the autostart service",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := newAutostartService()
		if err != nil {
			return err
		}
		switch err := svc.Disable(); {
		case errors.Is(err, autostart.ErrNotInstalled):
			fmt.Println("Autostart is not installed — nothing to remove.")
		case err != nil:
			return err
		default:
			fmt.Println("✓ Autostart disabled and unit removed.")
		}
		// Verify rather than assert: the acceptance criterion is that disable
		// leaves no orphan behind, so re-read the state and say what we see.
		st, err := svc.Status()
		if err != nil {
			return err
		}
		if st.Installed {
			return fmt.Errorf("unit still present at %s after disable — remove it by hand", st.UnitPath)
		}
		return nil
	},
}

var autostartStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report whether autostart is installed, running, and healthy",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := newAutostartService()
		if err != nil {
			return err
		}
		st, err := svc.Status()
		if err != nil {
			return err
		}
		renderAutostartStatus(os.Stdout, st)
		return nil
	},
}

// autostartRunCmd is the entry point the installed unit invokes. It is not
// meant to be typed by hand, but it is not hidden either: a user debugging why
// the service does nothing should be able to run exactly what the unit runs.
var autostartRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Entry point invoked by the autostart unit (starts the watcher)",
	Long: `Start the weft watcher the way the installed autostart unit does.

Waits for the home directory to become available (a user service can fire
before a network or encrypted $HOME is mounted), resolves the profile to run,
and enters the normal watch loop.

Exits 0 without starting anything when no profile is active — a non-zero exit
there would crash-loop under the unit's restart policy.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if configDir() == "" {
			return fmt.Errorf("resolving config directory")
		}
		cfgPath, err := config.FilePath()
		if err != nil {
			return err
		}
		// A user service can fire before a network or encrypted $HOME is
		// mounted. Wait for config.yaml rather than for $HOME: weft's own
		// logger calls MkdirAll under the home on every invocation, so the
		// directory's existence proves nothing about the mount.
		if waitErr := autostart.WaitForPath(cfgPath, autostartRunWait, homeProbeInterval); waitErr != nil {
			// Exit 0, not with an error: an unconfigured machine and an
			// unmounted home look identical from here, and failing would
			// crash-loop the unit on the former.
			slog.Warn("autostart: config file unavailable", slog.String("path", cfgPath))
			fmt.Printf("weft autostart: %s did not appear within %s — nothing to watch.\n", cfgPath, autostartRunWait)
			fmt.Println("If your home directory is on a network or encrypted volume, raise --wait-for-home in the unit.")
			return nil
		}

		name := autostartProfile
		if name == "" {
			active, err := config.ReadActiveProfile()
			if err != nil {
				return err
			}
			name = active
		}
		if name == "" {
			// Deliberate clean exit — see the command's Long text.
			slog.Info("autostart: no active profile, nothing to watch")
			fmt.Println("weft autostart: no active profile set — nothing to watch.")
			fmt.Println("Run 'weft profile use <name>' to activate one; the service will pick it up next start.")
			return nil
		}
		slog.Info("autostart: starting watcher", slog.String("profile", name))
		return runProfileUse(name, true)
	},
}

// newAutostartService builds the platform service rooted at weft's config dir.
func newAutostartService() (autostart.Service, error) {
	cfgDir := configDir()
	if cfgDir == "" {
		return nil, fmt.Errorf("resolving config directory")
	}
	return autostart.New(cfgDir)
}

// autostartOptions assembles the unit definition from the current process:
// which binary is running, which --config it was given, and the flags.
func autostartOptions() (autostart.Options, error) {
	exe, err := os.Executable()
	if err != nil {
		return autostart.Options{}, fmt.Errorf("resolving weft binary path: %w", err)
	}
	// Resolve symlinks so the unit pins the real binary. A unit pointing at a
	// symlink silently changes meaning when the link is re-targeted.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	opts := autostart.Options{
		BinPath: exe,
		Profile: autostartProfile,
		Linger:  autostartLinger,
	}
	// Carry --config through so an isolated config stays isolated after reboot.
	if cfgFile != "" {
		abs, err := filepath.Abs(locate.ExpandHome(cfgFile))
		if err != nil {
			return autostart.Options{}, fmt.Errorf("resolving config file path: %w", err)
		}
		opts.ConfigFile = abs
	}
	return opts, nil
}

// autostartProfileLabel describes the profile-selection mode for humans.
func autostartProfileLabel(pinned string) string {
	if pinned == "" {
		return "follow-active (resolved from config.yaml at start)"
	}
	return fmt.Sprintf("%s (pinned)", pinned)
}

// renderAutostartStatus prints the status report.
func renderAutostartStatus(w io.Writer, st autostart.Status) {
	fmt.Fprintf(w, "Mechanism: %s\n", st.Mechanism)
	if !st.Installed {
		fmt.Fprintln(w, "Autostart: not installed")
		fmt.Fprintln(w, "\nRun 'weft autostart enable' to start weft at login.")
		return
	}
	state := "installed, not running"
	if st.Running {
		state = "installed and running"
	}
	fmt.Fprintf(w, "Autostart: %s\n", state)
	fmt.Fprintf(w, "Unit:      %s\n", locate.Tilde(st.UnitPath))
	if st.BinPath != "" {
		suffix := ""
		if st.Stale {
			suffix = "  (stale)"
		}
		fmt.Fprintf(w, "Binary:    %s%s\n", locate.Tilde(st.BinPath), suffix)
	}
	fmt.Fprintf(w, "Profile:   %s\n", autostartProfileLabel(st.Profile))
	for _, n := range st.Notes {
		fmt.Fprintf(w, "\n! %s\n", n)
	}
}

// lingerHint is shown after enabling without --linger on platforms where user
// services stop at logout. Empty elsewhere.
var lingerHint = defaultLingerHint()

func init() {
	rootCmd.AddCommand(autostartCmd)
	autostartCmd.AddCommand(autostartEnableCmd, autostartDisableCmd, autostartStatusCmd, autostartRunCmd)

	autostartEnableCmd.Flags().StringVar(&autostartProfile, "profile", "",
		"pin the service to this profile (default: follow the active profile)")
	autostartEnableCmd.Flags().BoolVar(&autostartLinger, "linger", false,
		"keep the watcher running without an active login session (Linux only; changes loginctl policy)")

	autostartRunCmd.Flags().StringVar(&autostartProfile, "profile", "",
		"profile to run (default: active_profile from config.yaml)")
	autostartRunCmd.Flags().DurationVar(&autostartRunWait, "wait-for-home", defaultHomeWait,
		"how long to wait for config.yaml to become readable (a network or encrypted home may mount late)")
}
