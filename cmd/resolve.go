package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/source"
)

var resolveCmd = &cobra.Command{
	Use:   "resolve <target-path>",
	Short: "Reverse-lookup the source(s) that produced a target file",
	Long: `Resolve a target file path back to the weft source(s) that produced it.

Useful for debugging or scripting when you want to know which source owns a
file that was written to a harness config directory (e.g. ~/.claude/).

Examples:
  weft resolve ~/.claude/CLAUDE.md
  weft resolve ~/.claude/commands/deploy.md`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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

func init() {
	rootCmd.AddCommand(resolveCmd)
}
