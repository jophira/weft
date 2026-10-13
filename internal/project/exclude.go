package project

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExcludeEntry is the pattern weft adds so its repo-local state is invisible to
// git. Anchored with a leading slash so it matches only the repository root's
// .weft, never a directory of that name deeper in the tree.
const ExcludeEntry = "/.weft/"

// excludeHeader precedes the entry the first time weft adds it, so someone
// reading the file later knows what put it there and that removing it is safe.
const excludeHeader = "# added by weft: repo-local rule state, never committed"

// EnsureExcluded adds ExcludeEntry, plus any extra patterns, to the
// repository's .git/info/exclude, reporting whether it wrote anything.
//
// info/exclude rather than .gitignore, deliberately. .gitignore is a tracked
// file: editing it would put a weft line in everybody's diff and eventually in
// the repository's history, which is precisely the teammate-visible change this
// design refuses to make. info/exclude is per-clone, is never committed, and is
// exactly the mechanism git provides for "ignore this in my checkout only".
//
// extra carries the files weft writes for hook delivery. Those live at
// conventionally-ignored paths (Claude Code's .claude/settings.local.json), but
// a convention is not a rule: a fresh repository ignores nothing, so without
// this the "no tracked diff" delivery still leaves an untracked file in git
// status. Excluding what weft writes is what makes the promise true.
//
// Worktrees share the main checkout's info/exclude, since gitDirFor resolves the
// pointer. That is the behaviour we want: one entry covers every worktree. A
// submodule is its own repository, so it gets its own info/exclude.
func EnsureExcluded(root string, extra ...string) (bool, error) {
	gitDir, ok := gitDirFor(root)
	if !ok {
		// Not a git repository, so there is nothing for git to see. Not an error:
		// weft still manages the directory, it simply needs no exclusion.
		return false, nil
	}
	infoDir := filepath.Join(gitDir, "info")
	path := filepath.Join(infoDir, "exclude")

	data, err := os.ReadFile(path) //nolint:gosec // path is a resolved .git/info/exclude
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("project: reading %s: %w", path, err)
	}
	existing := string(data)

	missing := make([]string, 0, len(extra)+1)
	for _, pattern := range append([]string{ExcludeEntry}, normalisePatterns(extra)...) {
		if !hasExcludeEntry(existing, pattern) {
			missing = append(missing, pattern)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}

	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return false, fmt.Errorf("project: creating %s: %w", infoDir, err)
	}

	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	if existing != "" {
		b.WriteString("\n")
	}
	b.WriteString(excludeHeader)
	b.WriteString("\n")
	for _, pattern := range missing {
		b.WriteString(pattern)
		b.WriteString("\n")
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil { //nolint:gosec // git expects this file to be readable
		return false, fmt.Errorf("project: writing %s: %w", path, err)
	}
	return true, nil
}

// IsExcluded reports whether rel is listed in the repository's
// .git/info/exclude, in any spelling git treats as the same pattern.
//
// Weft writes that entry when it creates a delivery target, so "excluded" is the
// record that weft made the file. A repository that is not a git checkout, or
// has no info/exclude yet, excludes nothing.
func IsExcluded(root, rel string) bool {
	gitDir, ok := gitDirFor(root)
	if !ok {
		return false
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "info", "exclude")) //nolint:gosec // path is a resolved .git/info/exclude
	if err != nil {
		return false
	}
	patterns := normalisePatterns([]string{rel})
	return len(patterns) == 1 && hasExcludeEntry(string(data), patterns[0])
}

// Owned reports whether weft may write rel without anyone having opted in: the
// file does not exist yet, the directory is not a git checkout (so there is no
// teammate to surprise), or weft made it earlier and nobody has tracked it since.
//
// The last case needs two facts. The file is listed in info/exclude, which weft
// writes when it creates a delivery target. And git does not track it, because
// info/exclude is shared across worktrees and says nothing about a file a
// teammate has since committed upstream. Git is run only in that one case.
//
// Anything uncertain counts as not owned. A refused write is a reversible
// inconvenience; an edit to someone else's file is not.
func Owned(root, rel string) bool {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); os.IsNotExist(err) {
		return true
	}
	if _, ok := gitDirFor(root); !ok {
		return true
	}
	if !IsExcluded(root, rel) {
		return false
	}
	// --error-unmatch exits 1 when the path is untracked. Any other failure
	// (git missing, not a repository, a fatal error) tells us nothing about
	// tracking, so it is not read as "untracked".
	err := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", rel).Run() //nolint:gosec // root and rel come from the registry and harness specs
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 1
}

// normalisePatterns anchors each repo-relative path at the repository root, so
// an entry for ".claude/settings.local.json" cannot also match a same-named file
// deeper in the tree.
func normalisePatterns(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "/")
		if clean == "" || clean == "." {
			continue
		}
		out = append(out, "/"+clean)
	}
	return out
}

// hasExcludeEntry reports whether content already excludes pattern.
//
// Equivalent spellings count, because a user may have added their own before
// weft got here and a second near-identical line would be noise. Only exact
// patterns match: a comment mentioning the path is not an exclusion.
func hasExcludeEntry(content, pattern string) bool {
	want := equivalentSpellings(pattern)
	for _, line := range strings.Split(content, "\n") {
		if want[strings.TrimSpace(line)] {
			return true
		}
	}
	return false
}

// equivalentSpellings returns the forms of pattern git treats the same way here:
// with or without a leading slash, and with or without a trailing one.
func equivalentSpellings(pattern string) map[string]bool {
	bare := strings.Trim(pattern, "/")
	return map[string]bool{
		"/" + bare + "/": true,
		"/" + bare:       true,
		bare + "/":       true,
		bare:             true,
	}
}
