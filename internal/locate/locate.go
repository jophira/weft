package locate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Candidate describes one possible config-root location for a tool.
// Candidates are probed in declaration order; the first that exists wins.
type Candidate struct {
	// Path derives the absolute config-root path from the user's home dir
	// and the resolved XDG config dir (honours $XDG_CONFIG_HOME, falls back
	// to ~/.config). Return "" to skip this candidate.
	Path func(home, xdgConfig string) string
	// GOOS constrains this candidate to specific operating systems
	// ("linux", "darwin", "windows"). Empty means all platforms.
	GOOS []string
}

// First probes candidates in order and returns the first path that exists on
// disk. Reports false if none match.
func First(candidates []Candidate) (string, bool) {
	home, xdg := homeDirs()
	for _, c := range candidates {
		if !osMatch(c.GOOS) {
			continue
		}
		if p := c.Path(home, xdg); p != "" {
			if _, err := os.Stat(p); err == nil {
				return p, true
			}
		}
	}
	return "", false
}

// All returns every candidate path that matches the current OS, whether or not
// it exists on disk. Useful for building display strings.
func All(candidates []Candidate) []string {
	home, xdg := homeDirs()
	seen := map[string]bool{}
	var paths []string
	for _, c := range candidates {
		if !osMatch(c.GOOS) {
			continue
		}
		if p := c.Path(home, xdg); p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths
}

// Display formats OS-matching candidates as a human-readable string with tilde
// abbreviation, joined by "  or  ". Intended for the CONFIG column in target list.
func Display(candidates []Candidate) string {
	paths := All(candidates)
	parts := make([]string, len(paths))
	for i, p := range paths {
		parts[i] = Tilde(p)
	}
	return strings.Join(parts, "  or  ")
}

// ExpandHome replaces a leading ~/ with the user's absolute home directory.
// Paths that do not start with ~/ are returned unchanged.
//
// cf. Python: os.path.expanduser("~/foo")
func ExpandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

// Tilde replaces the user's home directory prefix with "~" for display.
//
// Output always uses forward slashes so the contracted form round-trips through
// ExpandHome (which only recognises a "~/" prefix) and stays portable when
// persisted to config or written into instruction files. On Unix this is a
// no-op; on Windows it normalises "\" to "/". cf. FileRegistry.Add, which stores
// Tilde(root) and later re-expands it.
func Tilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.ToSlash(path)
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~/" + filepath.ToSlash(path[len(home)+1:])
	}
	return filepath.ToSlash(path)
}

// HomeRel returns a Candidate whose path is home/rel on all platforms.
// Convenience constructor for the common case.
func HomeRel(rel ...string) Candidate {
	return Candidate{
		Path: func(home, _ string) string { return filepath.Join(append([]string{home}, rel...)...) },
	}
}

// XDGRel returns a Candidate whose path is xdgConfig/rel.
// Use for tools that follow the XDG Base Directory spec.
func XDGRel(rel ...string) Candidate {
	return Candidate{
		Path: func(_, xdg string) string { return filepath.Join(append([]string{xdg}, rel...)...) },
	}
}

func homeDirs() (home, xdg string) {
	home, _ = os.UserHomeDir()
	xdg = os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	return
}

func osMatch(goos []string) bool {
	if len(goos) == 0 {
		return true
	}
	for _, g := range goos {
		if g == runtime.GOOS {
			return true
		}
	}
	return false
}
