package harness

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one line of a coverage report.
type Entry struct {
	// Rel is the path relative to the config root, with a trailing slash on
	// directories so the two read differently at a glance.
	Rel string
	// Kind is what the entry carries.
	Kind FileKind
	// Desc describes it, for entries weft recognises.
	Desc string
	// Count is how many files a directory entry holds. Zero for single files.
	Count int
}

// Coverage is what weft manages in one harness root, against what is there.
type Coverage struct {
	Harness string
	Root    string
	// Exists is false when the config root is absent, which makes every other
	// field meaningless rather than merely empty.
	Exists bool
	// Declared is false when the harness has told weft nothing about its layout.
	// Distinct from "declared files are absent": the first means weft cannot say
	// anything about coverage, the second means it can and the answer is none.
	Declared bool
	// Managed are entries weft writes, per its manifest.
	Managed []Entry
	// Unmanaged are entries weft recognises and does not write. This is the
	// actionable half of the report: it is the answer to "what is weft missing".
	Unmanaged []Entry
	// Other counts top-level entries that are neither recognised nor known
	// harness state, and OtherNames lists them.
	Other      int
	OtherNames []string
}

// Audit compares a harness's config root against the paths weft's manifest
// claims, using the harness's own declaration of what it recognises.
//
// managed holds root-relative paths from the manifest. It is passed in rather
// than loaded here because the manifest lives in weft's config directory, which
// this package has no business knowing about.
//
// Directories are counted, not enumerated. A skills directory with nine bundles
// is one line saying nine, because the useful question is whether weft owns that
// class at all, not which files are in it.
func Audit(h Harness, root string, managed map[string]bool) Coverage {
	cov := Coverage{Harness: h.Name(), Root: root}
	if root == "" {
		return cov
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return cov
	}
	cov.Exists = true

	claimed := map[string]bool{} // top-level names accounted for
	for _, kf := range KnownFilesOf(h) {
		abs := filepath.Join(root, filepath.FromSlash(kf.Rel))
		fi, statErr := os.Stat(abs)
		if statErr != nil {
			continue // declared but not present in this installation
		}
		top := topSegment(kf.Rel)
		claimed[top] = true

		e := Entry{Rel: kf.Rel, Kind: kf.Kind, Desc: kf.Desc}
		if kf.Dir && fi.IsDir() {
			e.Rel += "/"
			e.Count = countFilesUnder(abs)
		}
		if isManaged(kf, managed) {
			cov.Managed = append(cov.Managed, e)
		} else {
			cov.Unmanaged = append(cov.Unmanaged, e)
		}
	}

	// Anything weft's manifest claims that no declaration covers still belongs in
	// the managed list, or the report would understate what weft owns.
	for rel := range managed {
		if coveredByKnown(rel, KnownFilesOf(h)) {
			continue
		}
		claimed[topSegment(rel)] = true
		cov.Managed = append(cov.Managed, Entry{Rel: rel, Kind: FileKind(""), Desc: "written by weft"})
	}

	state := StateEntriesOf(h)
	cov.Declared = len(KnownFilesOf(h)) > 0
	entries, readErr := os.ReadDir(root)
	if readErr == nil {
		for _, e := range entries {
			name := e.Name()
			if claimed[name] || IsSensitive(name) || matchesAny(name, state) {
				continue
			}
			cov.Other++
			cov.OtherNames = append(cov.OtherNames, name)
		}
	}

	sortEntries(cov.Managed)
	sortEntries(cov.Unmanaged)
	sort.Strings(cov.OtherNames)
	return cov
}

// matchesAny reports whether name equals or glob-matches any pattern.
//
// Globs matter because the noise is generational: settings.json.bak-20260801,
// goals_1.sqlite-wal. Listing each by name would leave the state declaration
// permanently out of date, and every stale entry inflates the count the report
// exists to keep small.
func matchesAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if p == name {
			return true
		}
		if ok, err := filepath.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}

// isManaged reports whether weft writes a declared entry. A directory counts as
// managed when weft owns any file inside it, since that is what "weft projects
// this class" means in practice.
func isManaged(kf KnownFile, managed map[string]bool) bool {
	rel := filepath.ToSlash(kf.Rel)
	if managed[rel] {
		return true
	}
	if !kf.Dir {
		return false
	}
	prefix := rel + "/"
	for m := range managed {
		if strings.HasPrefix(filepath.ToSlash(m), prefix) {
			return true
		}
	}
	return false
}

// coveredByKnown reports whether a manifest path already falls under a declared
// entry, so it is not listed twice.
func coveredByKnown(rel string, known []KnownFile) bool {
	slash := filepath.ToSlash(rel)
	for _, kf := range known {
		k := filepath.ToSlash(kf.Rel)
		if slash == k || strings.HasPrefix(slash, k+"/") {
			return true
		}
	}
	return false
}

// topSegment returns the first path segment, which is the name that appears in
// a directory listing of the root.
func topSegment(rel string) string {
	slash := filepath.ToSlash(rel)
	if i := strings.Index(slash, "/"); i >= 0 {
		return slash[:i]
	}
	return slash
}

// countFilesUnder returns the number of regular files beneath dir.
func countFilesUnder(dir string) int {
	count := 0
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			count++
		}
		return nil
	})
	return count
}

func sortEntries(in []Entry) {
	sort.Slice(in, func(i, j int) bool { return in[i].Rel < in[j].Rel })
}
