// Package project tracks the repositories weft has been used in, so
// project-scoped rules can be delivered without the daemon having to guess.
//
// The watcher may start at login and has no working directory of its own, and
// walking the disk looking for repositories is both slow and wrong (it would
// find every checkout the user has ever made, not the ones they work in). The
// registry sidesteps both: every weft command that runs inside a repository
// records it, so the set is a by-product of use. A repository the user stops
// visiting ages out on its own.
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// RegistryFileName is the registry's basename inside the config dir.
const RegistryFileName = "projects.yaml"

// StateDirName is weft's directory inside a repository. Everything weft writes
// into a repository lives here, and it is excluded from git via
// .git/info/exclude so it never reaches a teammate's diff.
const StateDirName = ".weft"

// DefaultMaxAge is how long a project stays registered after its last visit.
// Thirty days spans a holiday without dropping something still in use, and
// keeps the daemon's per-change work proportional to what the user touches.
const DefaultMaxAge = 30 * 24 * time.Hour

// Project is one repository weft has been used in.
//
// Root is the identity. Repo and Remote are attributes refreshed on every visit
// rather than keys, so renaming a remote updates the entry instead of orphaning
// it, and two clones of one repository are two entries because they are two
// working trees with two sets of files.
type Project struct {
	Root     string    `yaml:"root"`
	Repo     string    `yaml:"repo"`
	Remote   string    `yaml:"remote,omitempty"`
	Profile  string    `yaml:"profile,omitempty"`
	LastSeen time.Time `yaml:"last_seen"`
	// Enabled gates delivery. A registered project that is disabled is still
	// remembered (so the user is not asked twice) but is never written to.
	Enabled bool `yaml:"enabled"`
}

// Registry is the whole file.
type Registry struct {
	Projects []Project `yaml:"projects"`
}

// Path returns the registry file path inside cfgDir.
func Path(cfgDir string) string {
	return filepath.Join(cfgDir, RegistryFileName)
}

// Load reads the registry from cfgDir.
//
// Total by design, matching the resolver's philosophy: a missing file is an
// empty registry, and a corrupt one is reported as an error only so the caller
// can decide. Registration itself treats any failure as "no registry" rather
// than failing the command the user actually asked for.
func Load(cfgDir string) (*Registry, error) {
	data, err := os.ReadFile(Path(cfgDir))
	if err != nil {
		if os.IsNotExist(err) {
			return &Registry{}, nil
		}
		return nil, fmt.Errorf("project: reading registry: %w", err)
	}
	var r Registry
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("project: registry is corrupt: %w", err)
	}
	return &r, nil
}

// Save writes the registry to cfgDir atomically.
//
// The watcher and a foreground command both write this file, so a half-written
// document would be read back as corrupt and lose every entry.
func Save(cfgDir string, r *Registry) error {
	r.sort()
	data, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("project: marshalling registry: %w", err)
	}
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return fmt.Errorf("project: creating config dir: %w", err)
	}
	tmp, err := os.CreateTemp(cfgDir, RegistryFileName+".*")
	if err != nil {
		return fmt.Errorf("project: creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("project: writing registry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("project: closing temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("project: setting permissions: %w", err)
	}
	if err := os.Rename(tmpName, Path(cfgDir)); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("project: replacing registry: %w", err)
	}
	return nil
}

// sort orders entries by root so the file has a stable diff.
func (r *Registry) sort() {
	sort.Slice(r.Projects, func(i, j int) bool { return r.Projects[i].Root < r.Projects[j].Root })
}

// Get returns the entry for root, or nil.
func (r *Registry) Get(root string) *Project {
	for i := range r.Projects {
		if r.Projects[i].Root == root {
			return &r.Projects[i]
		}
	}
	return nil
}

// Upsert records a visit, reporting whether anything changed.
//
// The changed flag is what keeps registration cheap. Visiting a repository
// whose details are unchanged still updates LastSeen, but the caller can skip
// the write when nothing else moved and the timestamp is recent, so a statusline
// calling weft every second does not rewrite the file every second.
func (r *Registry) Upsert(p Project) bool {
	existing := r.Get(p.Root)
	if existing == nil {
		r.Projects = append(r.Projects, p)
		r.sort()
		return true
	}
	changed := existing.Repo != p.Repo || existing.Remote != p.Remote || existing.Profile != p.Profile
	existing.Repo = p.Repo
	existing.Remote = p.Remote
	if p.Profile != "" {
		existing.Profile = p.Profile
	}
	existing.LastSeen = p.LastSeen
	return changed
}

// Forget removes the entry for root, reporting whether one was present.
func (r *Registry) Forget(root string) bool {
	for i := range r.Projects {
		if r.Projects[i].Root == root {
			r.Projects = append(r.Projects[:i], r.Projects[i+1:]...)
			return true
		}
	}
	return false
}

// Stale reports whether p has not been visited within maxAge.
func (p Project) Stale(now time.Time, maxAge time.Duration) bool {
	if p.LastSeen.IsZero() {
		return true
	}
	return now.Sub(p.LastSeen) > maxAge
}

// Prune drops entries whose root no longer exists and entries not visited
// within maxAge, returning what it removed.
//
// Called on every write rather than from a command the user has to remember.
// A registry that needs manual gardening would simply rot.
//
// exists is injected so tests do not need a real filesystem; pass DirExists.
func (r *Registry) Prune(now time.Time, maxAge time.Duration, exists func(string) bool) []Project {
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	kept := make([]Project, 0, len(r.Projects))
	var dropped []Project
	for _, p := range r.Projects {
		if !exists(p.Root) || p.Stale(now, maxAge) {
			dropped = append(dropped, p)
			continue
		}
		kept = append(kept, p)
	}
	r.Projects = kept
	return dropped
}

// Active returns the enabled entries that are still within maxAge, which is the
// set the watcher refreshes on a source change.
func (r *Registry) Active(now time.Time, maxAge time.Duration) []Project {
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	out := make([]Project, 0, len(r.Projects))
	for _, p := range r.Projects {
		if p.Enabled && !p.Stale(now, maxAge) {
			out = append(out, p)
		}
	}
	return out
}

// DirExists reports whether path is a directory. The default Prune predicate.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
