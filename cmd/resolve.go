package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

var resolveTake string

var resolveCmd = &cobra.Command{
	Use:   "resolve <target-path> | <path> --take <harness>",
	Short: "Reverse-lookup a target file's source, or settle a conflict between harnesses",
	Long: `Without --take, resolve a target file path back to the weft source(s) that
produced it. Useful for debugging or scripting when you want to know which
source owns a file written to a harness config directory (e.g. ~/.claude/).

  weft resolve ~/.claude/CLAUDE.md
  weft resolve ~/.claude/commands/deploy.md

With --take, settle a conflict weft is holding. A conflict is a file two or
more harnesses have both changed since the last apply. Weft refuses to write
either side back on its own, because whichever it wrote last would erase the
other. Name the harness whose copy wins and weft brings the rest into line:

  weft resolve commands/review.md --take claude-code

The path is the one printed in the conflict report, relative to the staged
tree. The losing copies are backed up before they are rewritten, and none of
them is ever deleted. There is no --take merge: weft has no merge algorithm for
harness files, so combining two versions is a job for you and your editor.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if resolveTake != "" {
			return runResolveConflict(cmd.OutOrStdout(), args[0], resolveTake)
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

// runResolveConflict settles one held conflict by taking the named harness's
// copy.
//
// The conflict is re-detected here rather than read from a stored record: the
// user may have edited a third harness, or fixed the divergence by hand, since
// the report was printed. Acting on a stale record would rewrite files over a
// disagreement that no longer exists.
func runResolveConflict(out io.Writer, canonical, take string) error {
	cfgDir := configDir()
	if cfgDir == "" {
		return fmt.Errorf("resolving config directory")
	}
	profileName := activeProfileName()
	if profileName == "" {
		return fmt.Errorf("no active profile — run 'weft profile use <name>' first")
	}
	// resolveProfileRoots is what expands each source root; a raw registry read
	// hands back the stored "~/..." form, which no filesystem lookup will match.
	p, _, srcs, err := resolveProfileRoots(profileName)
	if err != nil {
		return err
	}

	targets, err := conflictTargets(cfgDir)
	if err != nil {
		return err
	}
	conflicts, err := harness.DetectConflicts(filepath.Join(cfgDir, "staged", profileName), targets)
	if err != nil {
		return err
	}
	want := filepath.ToSlash(filepath.Clean(canonical))
	var conflict harness.Conflict
	found := false
	for _, c := range conflicts {
		if c.Canonical == want {
			conflict, found = c, true
			break
		}
	}
	if !found {
		return noSuchConflict(want, conflicts)
	}

	srcPath, srcName := resolveConflictSource(conflict.Canonical, p, srcs)
	res, err := harness.Resolve(harness.ResolveRequest{
		Conflict: conflict, Take: take, SourcePath: srcPath, CfgDir: cfgDir,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "✓ %s resolved — took the %s copy\n", conflict.Canonical, res.Winner)
	for _, d := range res.Rewritten {
		fmt.Fprintf(out, "  ✓ %s ← %s\n", d.Harness, res.Winner)
	}
	fmt.Fprintf(out, "  previous copies kept in %s\n", locate.Tilde(res.BackupDir))
	if srcName != "" {
		fmt.Fprintf(out, "  source %q updated (%s)\n", srcName, locate.Tilde(srcPath))
	} else {
		fmt.Fprintln(out, "  no owning source found — the harness copies agree, but the next apply will "+
			"restore what the source holds; set write_back.default in your profile")
	}
	return nil
}

// noSuchConflict explains an unmatched path, listing what is actually held so
// the user can correct a typo without re-running the apply.
func noSuchConflict(want string, conflicts []harness.Conflict) error {
	if len(conflicts) == 0 {
		return fmt.Errorf("no conflicts to resolve — nothing is held")
	}
	names := make([]string, len(conflicts))
	for i, c := range conflicts {
		names[i] = c.Canonical
	}
	return fmt.Errorf("%s is not in conflict — weft is holding %s", want, strings.Join(names, ", "))
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
		"settle a held conflict by taking this harness's copy of the file")
	rootCmd.AddCommand(resolveCmd)
}
