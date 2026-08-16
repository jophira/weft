package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
)

// printCoverageReport renders the per-harness audit: what weft manages in each
// config root, against what is there.
//
// Only detected harnesses are reported. Auditing a harness that is not installed
// would print a wall of absent files and say nothing about coverage.
func printCoverageReport(out io.Writer, showOther bool) error {
	cfgDir := configDir()
	var reported bool

	for _, k := range harness.All() {
		if !k.H.Detect() {
			continue
		}
		reported = true
		root := harnessRoot(k)
		cov := harness.Audit(k.H, root, managedPaths(cfgDir, k.H.Name()))
		printCoverage(out, cov, showOther)
	}

	if !reported {
		fmt.Fprintln(out, "No harnesses detected.")
		return nil
	}
	if !showOther {
		fmt.Fprintln(out, "\n--all also lists unrecognised entries by name")
	}
	return nil
}

// harnessRoot resolves where a harness keeps its config, preferring the
// adapter's runtime answer over the static display string.
func harnessRoot(k harness.Known) string {
	if cp, ok := k.H.(harness.ConfigPather); ok {
		// ConfigPath is a display string and may offer several candidates; the
		// first is the one weft writes to.
		first, _, _ := strings.Cut(cp.ConfigPath(), "  or  ")
		return locate.ExpandHome(strings.TrimSpace(first))
	}
	return locate.ExpandHome(k.ConfigPath)
}

// managedPaths returns the root-relative paths weft's manifest claims for a
// harness. Sidecar keys are absolute paths outside the root, so they are
// converted where possible and dropped otherwise.
func managedPaths(cfgDir, name string) map[string]bool {
	out := map[string]bool{}
	m, err := manifest.Load(cfgDir, name)
	if err != nil || m.TargetRoot == "" {
		return out
	}
	for rel := range m.Files {
		if manifest.IsSidecarKey(rel) {
			continue
		}
		out[filepath.ToSlash(rel)] = true
	}
	if m.InstructionPath != "" {
		if rel, relErr := filepath.Rel(m.TargetRoot, m.InstructionPath); relErr == nil &&
			!strings.HasPrefix(rel, "..") {
			out[filepath.ToSlash(rel)] = true
		}
	}
	return out
}

// printProjectCoverage reports the instruction files present in a repository
// and what weft does with each.
//
// The same question as the global report, asked per repository: which files here
// carry rules, and is weft reading or writing them. Unlike the global plane the
// interesting answer is usually "read, never written", because project delivery
// only writes where the harness leaves it no cheaper option.
func printProjectCoverage(out io.Writer, root string) {
	detected := detectedHarnesses()
	if len(detected) == 0 {
		return
	}
	inputs := harness.ProjectInputs(detected)
	if len(inputs) == 0 {
		return
	}

	written := map[string]string{} // repo-relative path -> harness that writes it
	for _, h := range detected {
		if spec := harness.ProjectSupportOf(h); spec.Delivery.TracksGit() {
			written[filepath.ToSlash(spec.Path)] = h.Name()
		}
	}

	var rows []string
	for _, pattern := range inputs {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			continue
		}
		for _, m := range matches {
			rel, relErr := filepath.Rel(root, m)
			if relErr != nil {
				continue
			}
			slash := filepath.ToSlash(rel)
			role := "read as project input"
			if who, ok := written[slash]; ok {
				role = "written for " + who
			}
			rows = append(rows, fmt.Sprintf("      %s\t%s", slash, role))
		}
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "  files:   none of the recognised project instruction files are present")
		return
	}
	fmt.Fprintln(out, "  files:")
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, r := range dedupeSortedStrings(rows) {
		fmt.Fprintln(w, r)
	}
	_ = w.Flush()
}

// dedupeSortedStrings returns the unique values of in, sorted.
func dedupeSortedStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func printCoverage(out io.Writer, cov harness.Coverage, showOther bool) {
	fmt.Fprintf(out, "\n%s  %s\n", cov.Harness, locate.Tilde(cov.Root))
	if !cov.Exists {
		fmt.Fprintln(out, "  config root not found")
		return
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	writeSection(w, "✓ managed", cov.Managed)
	writeSection(w, "~ unmanaged", cov.Unmanaged)
	_ = w.Flush()

	if len(cov.Managed) == 0 && len(cov.Unmanaged) == 0 {
		// Two different answers, kept apart. "Weft does not know this layout" is a
		// gap in weft; "weft knows it and none of it is here" is a fact about the
		// installation. Collapsing them would hide the first behind the second.
		if cov.Declared {
			fmt.Fprintln(out, "  no recognised files present")
		} else {
			fmt.Fprintln(out, "  layout not declared to weft, so coverage is unknown")
		}
	}
	if cov.Other > 0 {
		if showOther {
			fmt.Fprintf(out, "  · other (%d): %s\n", cov.Other, strings.Join(cov.OtherNames, ", "))
		} else {
			fmt.Fprintf(out, "  · other: %d unrecognised entr%s\n", cov.Other, plural(cov.Other, "y", "ies"))
		}
	}
}

func writeSection(w io.Writer, title string, entries []harness.Entry) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprintf(w, "  %s\n", title)
	for _, e := range entries {
		count := ""
		if e.Count > 0 {
			count = fmt.Sprintf("%d file%s", e.Count, plural(e.Count, "", "s"))
		}
		fmt.Fprintf(w, "      %s\t%s\t%s\t%s\n", e.Rel, e.Kind, count, e.Desc)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
