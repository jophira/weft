package rules

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Context is the set of repository signals a rule's Detect predicate is
// evaluated against. It is intentionally small: root-level file names plus
// declared dependency identifiers — the signals real-world detection needs
// (go.mod present, `vue` in package.json, spring-boot in pom.xml) without
// walking or hashing the whole tree.
type Context struct {
	// Files holds the base names of the repository root's top-level files.
	// Detection signals are root-level by convention (go.mod, pom.xml,
	// package.json, build.gradle), keeping detection O(root entries).
	Files []string
	// Deps holds dependency identifiers gathered from recognised manifests:
	// package.json (dependencies + devDependencies keys) and pom.xml
	// (each dependency's groupId and artifactId).
	Deps []string
	// Repo is the repository directory's base name (e.g. "weft"), letting a
	// project-scoped rule detect by identity: detect: repo == "weft".
	Repo string
	// Remote is the origin URL from <repo>/.git/config, or empty when there is
	// no parseable origin. Enables detect: remote.contains("jophira/weft").
	Remote string
}

// manifest file names inspected for dependency identifiers.
const (
	packageJSONFile = "package.json"
	pomXMLFile      = "pom.xml"
)

// BuildContext inspects repoRoot and returns the signals available for
// detection. It is resilient: unreadable or malformed manifests contribute no
// dependencies rather than failing the build, so detection degrades to "not
// matched" instead of erroring.
func BuildContext(repoRoot string) (Context, error) {
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return Context{}, err
	}

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	deps := make([]string, 0)
	if slices.Contains(files, packageJSONFile) {
		deps = append(deps, npmDeps(filepath.Join(repoRoot, packageJSONFile))...)
	}
	if slices.Contains(files, pomXMLFile) {
		deps = append(deps, mavenDeps(filepath.Join(repoRoot, pomXMLFile))...)
	}

	return Context{
		Files:  files,
		Deps:   dedupeSorted(deps),
		Repo:   filepath.Base(repoRoot),
		Remote: gitOriginRemote(repoRoot),
	}, nil
}

// gitOriginRemote returns the origin remote URL from <repoRoot>/.git/config, or
// empty when .git is absent, is a worktree/submodule pointer file, or has no
// origin. Parsing the config file directly avoids shelling out to git and works
// even when git is not installed.
func gitOriginRemote(repoRoot string) string {
	gitPath := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(gitPath)
	if err != nil || !info.IsDir() {
		// No .git, or a worktree/submodule ".git" file (gitdir pointer) — the
		// latter's config layout is out of scope; degrade to no remote.
		return ""
	}
	data, err := os.ReadFile(filepath.Join(gitPath, "config")) //nolint:gosec // path is <repoRoot>/.git/config
	if err != nil {
		return ""
	}
	return parseOriginURL(string(data))
}

// parseOriginURL extracts the url of the [remote "origin"] section from a git
// config file body, or "" when absent.
func parseOriginURL(cfg string) string {
	inOrigin := false
	for _, line := range strings.Split(cfg, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inOrigin = t == `[remote "origin"]`
			continue
		}
		if inOrigin {
			if key, val, ok := strings.Cut(t, "="); ok && strings.TrimSpace(key) == "url" {
				return strings.TrimSpace(val)
			}
		}
	}
	return ""
}

// npmDeps returns the dependency and devDependency names declared in a
// package.json, or nil when the file is missing or malformed.
func npmDeps(path string) []string {
	data, err := os.ReadFile(path) //nolint:gosec // path is repoRoot/package.json
	if err != nil {
		return nil
	}
	// cf. Python: json.load into a dataclass — here a struct with only the
	// fields we care about; unknown keys are ignored.
	var pkg struct {
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	out := make([]string, 0, len(pkg.Dependencies)+len(pkg.DevDependencies))
	for name := range pkg.Dependencies {
		out = append(out, name)
	}
	for name := range pkg.DevDependencies {
		out = append(out, name)
	}
	return out
}

// mavenDeps returns the groupId and artifactId of every dependency declared in
// a pom.xml, or nil when the file is missing or malformed. Both identifiers are
// emitted so predicates can match either coordinate
// (e.g. deps.exists(d, d.contains("spring-boot"))).
func mavenDeps(path string) []string {
	data, err := os.ReadFile(path) //nolint:gosec // path is repoRoot/pom.xml
	if err != nil {
		return nil
	}
	var project struct {
		Dependencies struct {
			Dependency []struct {
				GroupID    string `xml:"groupId"`
				ArtifactID string `xml:"artifactId"`
			} `xml:"dependency"`
		} `xml:"dependencies"`
	}
	if err := xml.Unmarshal(data, &project); err != nil {
		return nil
	}
	out := make([]string, 0, len(project.Dependencies.Dependency)*2)
	for _, d := range project.Dependencies.Dependency {
		if g := strings.TrimSpace(d.GroupID); g != "" {
			out = append(out, g)
		}
		if a := strings.TrimSpace(d.ArtifactID); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// dedupeSorted returns the unique values of in, sorted, with empties removed.
func dedupeSorted(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
