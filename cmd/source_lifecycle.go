package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/homemove"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/source"
)

var sourceRelocateTo string

var sourceRelocateCmd = &cobra.Command{
	Use:   "relocate <name>",
	Short: "Move a source's content into the weft workbench and repoint the registry",
	Long: `Relocate a registered source's root into ~/weft/sources/<name> (or --to
<dir>), update the registry to the new location, and leave a symlink bridge at
the old path so existing references keep resolving.

Non-destructive (move, never delete; refuses to clobber a populated destination)
and idempotent — re-running once relocated is a no-op.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		reg, err := newRegistry()
		if err != nil {
			return err
		}
		s, err := reg.Get(name)
		if err != nil {
			return err
		}
		oldRoot := locate.ExpandHome(s.Root)

		dst := sourceRelocateTo
		if dst == "" {
			home := weftHomeDir()
			if home == "" {
				return fmt.Errorf("cannot resolve weft home directory")
			}
			dst = filepath.Join(home, "sources", name)
		} else if dst, err = expandAndAbs(dst); err != nil {
			return fmt.Errorf("resolving --to: %w", err)
		}

		out := cmd.OutOrStdout()
		res, err := relocateSource(reg, name, dst)
		if err != nil {
			return err
		}
		if !res.Moved {
			fmt.Fprintf(out, "source %q: %s\n", name, res.SkipReason)
			return nil
		}
		bridged := ""
		if res.Bridged {
			bridged = fmt.Sprintf(" (bridge left at %s)", oldRoot)
		}
		fmt.Fprintf(out, "  relocated %q -> %s%s\n", name, dst, bridged)
		fmt.Fprintf(out, "✓ source %q now rooted at %s\n", name, locate.Tilde(dst))
		return nil
	},
}

// relocateSource moves source name's content to dst, updates the registry entry
// to the new root, and leaves a symlink bridge at the old path. Idempotent (a
// no-op once relocated). Shared by 'weft source relocate' and 'weft migrate'.
func relocateSource(reg *source.FileRegistry, name, dst string) (homemove.Result, error) {
	s, err := reg.Get(name)
	if err != nil {
		return homemove.Result{}, err
	}
	src := locate.ExpandHome(s.Root)
	res, err := homemove.Move(src, dst, true)
	if err != nil {
		return res, err
	}
	if res.Moved {
		s.Root = dst
		if err := reg.Update(*s); err != nil {
			return res, fmt.Errorf("content moved, but updating the registry failed: %w", err)
		}
	}
	return res, nil
}

var sourceRenameCmd = &cobra.Command{
	Use:   "rename <old> <new>",
	Short: "Rename a source and update every profile that references it",
	Long: `Rename a registered source. Crucially, this also rewrites every profile
that references the old name (its sources list and write-back config), so a
rename never silently orphans a profile — the failure mode that leaves a profile
pointing at a source that no longer exists.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		oldName, newName := args[0], args[1]
		if oldName == newName {
			return fmt.Errorf("old and new names are the same")
		}
		reg, err := newRegistry()
		if err != nil {
			return err
		}
		s, err := reg.Get(oldName)
		if err != nil {
			return err
		}
		if _, err := reg.Get(newName); err == nil {
			return fmt.Errorf("source %q already exists", newName)
		}

		s.Name = newName
		if err := reg.Add(*s); err != nil { // validates the new name
			return err
		}
		if err := reg.Remove(oldName); err != nil {
			_ = reg.Remove(newName) // roll back the just-added entry
			return fmt.Errorf("renaming source: %w", err)
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "✓ source renamed %q -> %q\n", oldName, newName)

		updated, err := renameSourceInProfiles(oldName, newName)
		if err != nil {
			return fmt.Errorf("source renamed, but updating profiles failed: %w", err)
		}
		if len(updated) == 0 {
			fmt.Fprintln(out, "  no profiles referenced it")
		}
		for _, p := range updated {
			fmt.Fprintf(out, "  updated profile %q\n", p)
		}
		return nil
	},
}

// renameSourceInProfiles rewrites old -> new in every profile's sources list and
// write-back config, persisting only the profiles that changed. Returns the
// names of the profiles it updated.
func renameSourceInProfiles(oldName, newName string) ([]string, error) {
	pm, err := newProfileManager()
	if err != nil {
		return nil, err
	}
	profiles, err := pm.List()
	if err != nil {
		return nil, err
	}
	var updated []string
	for _, p := range profiles {
		changed := false
		for i, src := range p.Sources {
			if src == oldName {
				p.Sources[i] = newName
				changed = true
			}
		}
		if p.WriteBack.Default == oldName {
			p.WriteBack.Default = newName
			changed = true
		}
		for k, v := range p.WriteBack.Overrides {
			if v == oldName {
				p.WriteBack.Overrides[k] = newName
				changed = true
			}
		}
		if changed {
			if err := pm.Update(p); err != nil {
				return updated, fmt.Errorf("updating profile %q: %w", p.Name, err)
			}
			updated = append(updated, p.Name)
		}
	}
	return updated, nil
}

func init() {
	sourceRelocateCmd.Flags().StringVar(&sourceRelocateTo, "to", "", "destination directory (default: ~/weft/sources/<name>)")
	sourceCmd.AddCommand(sourceRelocateCmd, sourceRenameCmd)
}
