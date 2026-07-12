package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/instruction"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/runstate"
)

var statusShort bool

// harnessStatus is the projection state of one harness derived from its manifest.
type harnessStatus struct {
	Harness   string
	Profile   string
	InstrPath string
	Drift     string // "ok", "drift", "missing", or "n/a"
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the active profile and per-harness projection state",
	Long: `Report the active weft profile and, for every harness weft has applied
to, the profile it carries and whether its managed instruction block has drifted
from what weft last wrote (i.e. was edited outside weft).

Use --short for a single-line summary suitable for a shell prompt or a harness
status line (e.g. Claude Code's statusLine command).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgDir, err := config.DefaultDir()
		if err != nil {
			return fmt.Errorf("locating config dir: %w", err)
		}
		statuses, err := collectHarnessStatus(cfgDir)
		if err != nil {
			return err
		}
		rs, err := runstate.Read(cfgDir)
		if err != nil {
			return fmt.Errorf("reading watcher runstate: %w", err)
		}
		renderStatus(os.Stdout, activeProfileName(), rs, statuses, statusShort)
		return nil
	},
}

// collectHarnessStatus scans cfgDir/manifests and returns the projection state
// of every harness weft has applied to, sorted by harness name.
func collectHarnessStatus(cfgDir string) ([]harnessStatus, error) {
	entries, err := os.ReadDir(filepath.Join(cfgDir, "manifests"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading manifests dir: %w", err)
	}

	var out []harnessStatus
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		m, err := manifest.Load(cfgDir, name)
		if err != nil {
			continue
		}
		out = append(out, harnessStatus{
			Harness:   name,
			Profile:   m.Profile,
			InstrPath: m.InstructionPath,
			Drift:     instructionDrift(m),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Harness < out[j].Harness })
	return out, nil
}

// instructionDrift compares the on-disk managed block to the hash weft recorded.
//   - "n/a"     — harness has no managed instruction block (e.g. Warp)
//   - "missing" — the file or its managed block is gone
//   - "drift"   — the block was edited outside weft
//   - "ok"      — the block matches what weft last wrote
func instructionDrift(m *manifest.Manifest) string {
	if m.InstructionPath == "" || m.InstructionBlock == "" {
		return "n/a"
	}
	data, err := os.ReadFile(m.InstructionPath)
	if err != nil {
		return "missing"
	}
	body, found := instruction.Extract(data)
	if !found {
		return "missing"
	}
	if manifest.HashBytes([]byte(body)) == m.InstructionBlock {
		return "ok"
	}
	return "drift"
}

// renderStatus writes the status report to w. short emits a single summary line.
// rs is the live watcher's runstate, or nil when no watcher is running.
func renderStatus(w io.Writer, active string, rs *runstate.RunState, statuses []harnessStatus, short bool) {
	if active == "" {
		active = "none"
	}

	if short {
		drift := 0
		for _, s := range statuses {
			if s.Drift == "drift" {
				drift++
			}
		}
		watch := "off"
		if rs != nil {
			watch = "on"
		}
		fmt.Fprintf(w, "weft: %s · %d harness · drift:%d · watch:%s\n", active, len(statuses), drift, watch)
		return
	}

	fmt.Fprintf(w, "Active profile: %s\n", active)
	if rs != nil {
		fmt.Fprintf(w, "Watcher: running (pid %d, profile %q, up %s)\n", rs.PID, rs.Profile, fmtUptime(rs.Uptime()))
	} else {
		fmt.Fprintln(w, "Watcher: not running")
	}
	if len(statuses) == 0 {
		fmt.Fprintln(w, "No harnesses applied yet. Run 'weft profile use <name>'.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "HARNESS\tPROFILE\tINSTRUCTION\tBLOCK")
	for _, s := range statuses {
		instr := s.InstrPath
		if instr == "" {
			instr = "-"
		} else {
			instr = locate.Tilde(instr)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Harness, dashIfEmpty(s.Profile), instr, s.Drift)
	}
	_ = tw.Flush()
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// fmtUptime renders a watcher uptime compactly: "45s", "12m", "2h13m", "3d4h".
func fmtUptime(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

func init() {
	statusCmd.Flags().BoolVar(&statusShort, "short", false, "print a one-line summary for a shell prompt or harness status line")
	rootCmd.AddCommand(statusCmd)
}
