package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

var (
	resolveTake   string
	resolveMerged string
	resolveYes    bool
)

var resolveCmd = &cobra.Command{
	Use:   "resolve [<target-path> | <path> --take <harness>]",
	Short: "Reverse-lookup a target file's source, or settle a conflict between harnesses",
	Long: `Without --take, resolve a target file path back to the weft source(s) that
produced it. Useful for debugging or scripting when you want to know which
source owns a file written to a harness config directory (e.g. ~/.claude/).

  weft resolve ~/.claude/CLAUDE.md
  weft resolve ~/.claude/commands/deploy.md

With --take, settle a conflict weft is holding. A conflict is a file, or one
source's instruction section, that two or more harnesses have both changed
since the last apply. Weft refuses to write either side back on its own,
because whichever it wrote last would erase the other. Name the harness whose
copy wins and weft brings the rest into line:

  weft resolve commands/review.md --take claude-code
  weft resolve instructions:pers-tech --take codex

The path is the one printed in the conflict report, relative to the staged
tree. The losing copies are backed up before they are rewritten, and none of
them is ever deleted.

--take merge takes the changes from every harness instead of discarding one
side, and opens the result in $EDITOR. Review is not optional and not skippable
when the merge comes out clean: a clean merge is where two rules that
contradict each other slip past unnoticed. Merge therefore needs a terminal;
--take <harness> stays fully non-interactive for scripts.

With no arguments and a terminal, resolve walks every held conflict and offers
the same choices one at a time. With --yes, or with stdin redirected, it reports
what is held and exits non-zero instead of asking.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			// A flag with nothing to apply it to is a mistyped command, not a
			// request to walk everything. Dropping into the interactive loop here
			// would answer a question the user did not ask.
			if resolveTake != "" || resolveMerged != "" {
				return fmt.Errorf(
					"--take and --merged settle one named conflict, so they need the path from the conflict " +
						"report (e.g. 'weft resolve commands/review.md --take codex'); " +
						"run 'weft resolve' with no flags to walk every held conflict")
			}
			return runResolveWalk(cmd.InOrStdin(), cmd.OutOrStdout())
		}
		if resolveTake != "" || resolveMerged != "" {
			return runResolveConflict(cmd.OutOrStdout(), args[0], resolveTake, resolveMerged)
		}
		targetPath, err := expandAndAbs(args[0])
		if err != nil {
			return fmt.Errorf("resolving path: %w", err)
		}

		cfgDir := configDir()
		if cfgDir == "" {
			return fmt.Errorf("resolving config directory")
		}

		m, rel, err := findManifest(cfgDir, targetPath)
		if err != nil {
			return err
		}
		if m == nil {
			return fmt.Errorf("%s is not managed by weft", locate.Tilde(targetPath))
		}

		fmt.Printf("%s\n", locate.Tilde(targetPath))
		fmt.Printf("  harness: %s\n", m.Harness)
		fmt.Printf("  profile: %s\n", m.Profile)

		// Determine contributing source(s).
		sources, ok := m.SourceFiles[rel]
		if ok && len(sources) > 0 {
			// Merged file: multiple sources contributed.
			fmt.Printf("  sources: %s (merged)\n", strings.Join(sources, ", "))
		} else {
			// Single-source file: scan source roots to find which one owns it.
			src, srcPath, findErr := findSingleSource(cfgDir, m.Profile, rel)
			if findErr != nil {
				fmt.Printf("  source:  (could not resolve — %v)\n", findErr)
			} else if src == "" {
				fmt.Printf("  source:  (not found in any source root)\n")
			} else {
				fmt.Printf("  source:  %s\n", src)
				fmt.Printf("  path:    %s\n", locate.Tilde(srcPath))
			}
		}
		return nil
	},
}

// expandAndAbs expands ~ and resolves to an absolute path.
func expandAndAbs(p string) (string, error) {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[2:])
	}
	return filepath.Abs(p)
}

// findManifest scans all harness manifests in cfgDir/manifests/ and returns
// the one whose TargetRoot is a prefix of targetPath, along with the relative
// path within that root. Returns nil manifest when no match is found.
func findManifest(cfgDir, targetPath string) (*manifest.Manifest, string, error) {
	manifestsDir := filepath.Join(cfgDir, "manifests")
	entries, err := os.ReadDir(manifestsDir)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("reading manifests dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		harnessName := strings.TrimSuffix(e.Name(), ".json")
		m, err := manifest.Load(cfgDir, harnessName)
		if err != nil || m.TargetRoot == "" {
			continue
		}
		root := m.TargetRoot
		if targetPath == root || strings.HasPrefix(targetPath, root+string(filepath.Separator)) {
			rel, err := filepath.Rel(root, targetPath)
			if err != nil {
				continue
			}
			if _, owned := m.Files[rel]; owned {
				return m, rel, nil
			}
			// Path is under the target root but not in the manifest — not managed.
			return nil, "", nil
		}
	}
	return nil, "", nil
}

// findSingleSource loads the profile's sources and finds which source root
// contains rel. Returns the source name and absolute source path.
func findSingleSource(_, profileName, rel string) (name, absPath string, err error) {
	pm, err := newProfileManager()
	if err != nil {
		return "", "", err
	}
	p, err := pm.Get(profileName)
	if err != nil {
		return "", "", fmt.Errorf("loading profile %q: %w", profileName, err)
	}

	reg, err := newRegistry()
	if err != nil {
		return "", "", err
	}
	srcs, listErr := reg.List()
	if listErr != nil {
		return "", "", fmt.Errorf("listing sources: %w", listErr)
	}

	// Build a map from source name → Source for the profile's sources.
	srcMap := make(map[string]source.Source, len(srcs))
	for _, s := range srcs {
		srcMap[s.Name] = s
	}

	for _, name := range p.Sources {
		s, ok := srcMap[name]
		if !ok {
			continue
		}
		candidate := filepath.Join(s.Root, rel)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return name, candidate, nil
		}
	}
	return "", "", nil
}

// runResolveConflict settles one named conflict: by taking one harness's copy,
// or by merging every side and applying what the user saved in the editor.
func runResolveConflict(out io.Writer, label, take, mergedPath string) error {
	cfgDir, profileName, err := resolveContext()
	if err != nil {
		return err
	}
	held, err := collectHeldConflicts(cfgDir, profileName)
	if err != nil {
		return err
	}
	h, err := findHeld(held, label)
	if err != nil {
		return err
	}

	// --merged finishes a review that started earlier: mergeAndReview writes the
	// work file and prints this command when there is no $EDITOR to open.
	if mergedPath != "" {
		path, pErr := expandAndAbs(mergedPath)
		if pErr != nil {
			return fmt.Errorf("resolving path: %w", pErr)
		}
		data, rErr := os.ReadFile(path) //nolint:gosec // a path the user named on the command line
		if rErr != nil {
			return fmt.Errorf("reading the reviewed merge: %w", rErr)
		}
		// The conflict h describes was detected a moment ago; the reviewed file may
		// have been merged out of something older. Its stamp is what says which.
		if from, stamped := mergeFingerprint(string(data)); stamped && from != h.fingerprint() {
			return fmt.Errorf(
				"%s has changed since that merge was prepared, so applying it would discard the newer "+
					"edit; re-run 'weft resolve %s --take merge' to merge against what is there now",
				h.Label, h.Label)
		}
		reviewed := strings.TrimLeft(stripMergeHeader(string(data)), "\n")
		return settleHeld(out, h, mergeWinner, []byte(reviewed))
	}

	if strings.EqualFold(strings.TrimSpace(take), mergeWinner) {
		if !isInteractiveTTY() {
			return errMergeNeedsTTY
		}
		merged, mErr := mergeAndReview(out, cfgDir, h, time.Now())
		if mErr != nil {
			return mErr
		}
		if cErr := confirmUnchanged(cfgDir, profileName, h); cErr != nil {
			return fmt.Errorf("%w (your review is kept at %s)", cErr, locate.Tilde(mergeWorkFile(cfgDir, h.Label)))
		}
		return settleHeld(out, h, mergeWinner, merged)
	}
	return settleHeld(out, h, take, nil)
}

// runResolveWalk is `weft resolve` with nothing named: the interactive loop when
// a human is there to answer, a report and a non-zero exit when nobody is.
func runResolveWalk(in io.Reader, out io.Writer) error {
	cfgDir, profileName, err := resolveContext()
	if err != nil {
		return err
	}
	held, err := collectHeldConflicts(cfgDir, profileName)
	if err != nil {
		return err
	}
	if len(held) == 0 {
		fmt.Fprintln(out, "✓ no conflicts held")
		return nil
	}
	if resolveYes || !isInteractiveTTY() {
		reportHeld(out, held, time.Now())
		return errConflictsHeld
	}
	return resolveLoop(in, out, cfgDir, profileName, held)
}

// resolveContext resolves the two things every settlement needs, with the same
// message whichever entry point asked.
func resolveContext() (cfgDir, profileName string, err error) {
	cfgDir = configDir()
	if cfgDir == "" {
		return "", "", fmt.Errorf("resolving config directory")
	}
	profileName = activeProfileName()
	if profileName == "" {
		return "", "", fmt.Errorf("no active profile — run 'weft profile use <name>' first")
	}
	return cfgDir, profileName, nil
}

// resolveConflictSource finds the source file the winning content must be
// written to, so the next apply re-projects the resolution rather than undoing
// it. Returns empty strings when no source owns the path.
func resolveConflictSource(canonical string, p *profile.Profile, srcs []source.Source) (path, name string) {
	srcName, srcPath, ok := owningSource(filepath.FromSlash(canonical), p, srcs)
	if !ok {
		return "", ""
	}
	return srcPath, srcName
}

func init() {
	resolveCmd.Flags().StringVar(&resolveTake, "take", "",
		"settle a held conflict by taking this harness's copy, or \"merge\" to combine every side and review the result")
	resolveCmd.Flags().StringVar(&resolveMerged, "merged", "",
		"apply an already-reviewed merge from this file (printed by --take merge when $EDITOR is unset)")
	resolveCmd.Flags().BoolVar(&resolveYes, "yes", false,
		"never prompt: report what is held and exit non-zero")
	rootCmd.AddCommand(resolveCmd)
}
