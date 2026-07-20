package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/mcpconfig"
)

// Adoption is the fan-in counterpart to apply: it takes a file the user authored
// directly inside a harness (a new ~/.claude/agents/reviewer.md) and copies it
// into a weft source, so every other harness can receive it on the next apply.
//
// It is deliberately explicit — never a watcher side effect. Adoption is a
// one-way door: once a source owns the file, weft overwrites it on every
// subsequent apply. Doing that implicitly would silently transfer authorship of
// a file the user believes is theirs (ADR 0004 D3).

// ErrConfirmRequired is returned by Adopt when the caller has not recorded the
// user's approval. The gate lives here rather than in the cobra handler so every
// caller — CLI, future API, tests — passes through it (cf. issue #82, where a
// confirmation enforced only at the CLI layer was bypassable).
//
// cf. Java: a checked exception the caller must handle before retrying with the
// approved flag set.
var ErrConfirmRequired = errors.New("adoption requires confirmation")

// adoptableExt is the only file extension weft adopts. Commands, agents and
// skills are markdown by definition; a binary or script in a skill bundle is an
// asset whose provenance weft cannot reason about, so it stays where it is.
const adoptableExt = ".md"

// AdoptableClasses is the conservative allowlist of classes weft will adopt.
//
// Instructions are excluded because weft already owns a managed block inside the
// harness's instruction file — adopting the whole file would claim the user's
// prose around it. MCP is excluded because native MCP config is not a file copy;
// it travels through the canonical form in internal/mcpconfig. ClassOther is
// excluded because "weft could not classify it" is never a reason to take it.
func AdoptableClasses() []Class { return []Class{ClassCommands, ClassAgents, ClassSkills} }

// skipDirs are directory names never descended into during a scan. These hold
// harness runtime state and weft's own bookkeeping, not authored rules, and a
// scan that surfaces them is a scan users stop reading.
var skipDirs = map[string]struct{}{
	".git":            {},
	"backups":         {},
	"caches":          {},
	"history":         {},
	"ide":             {},
	"logs":            {},
	"node_modules":    {},
	"plugins":         {},
	"projects":        {},
	"shell-snapshots": {},
	"statsig":         {},
	"todos":           {},
}

// ScanTarget is one harness's on-disk state as seen by a scan.
type ScanTarget struct {
	Harness string
	Root    string            // absolute path of the harness config root
	Owned   map[string]string // manifest Files: target-relative path -> hash
	H       Harness           // the adapter, or nil when weft has no adapter for it
	CfgDir  string            // weft config dir, for recording ownership on adopt
}

// Candidate is a file present in a harness but owned by no source.
type Candidate struct {
	Harness string
	Class   Class
	Rel     string // relative to the harness root — the path the user passes to adopt
	Abs     string
}

// Scan reports every adoptable file under each target that no manifest owns.
//
// Ownership is checked against the manifest rather than the filesystem because
// that is weft's only record of authorship: a file weft wrote and a file the
// user wrote are indistinguishable on disk.
func Scan(targets []ScanTarget) ([]Candidate, error) {
	var out []Candidate
	for _, t := range targets {
		if t.Root == "" {
			continue
		}
		err := filepath.WalkDir(t.Root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// A directory weft cannot read is not a reason to abandon the whole
				// scan — report what is readable and move on.
				// cf. Python: os.walk(onerror=None) swallowing per-dir errors.
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			rel, relErr := filepath.Rel(t.Root, path)
			if relErr != nil {
				return relErr
			}
			if d.IsDir() {
				if _, skip := skipDirs[d.Name()]; skip || (rel != "." && strings.HasPrefix(d.Name(), ".")) {
					return fs.SkipDir
				}
				return nil
			}
			c, ok := adoptableCandidate(rel, t)
			if !ok {
				return nil
			}
			out = append(out, Candidate{Harness: t.Harness, Class: c, Rel: rel, Abs: path})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", t.Harness, err)
		}
	}
	return out, nil
}

// adoptableCandidate reports the class of rel if it is an unowned, adoptable
// file within t, and ok=false otherwise.
func adoptableCandidate(rel string, t ScanTarget) (Class, bool) {
	if _, owned := t.Owned[rel]; owned {
		return "", false
	}
	if !strings.EqualFold(filepath.Ext(rel), adoptableExt) {
		return "", false
	}
	c := nativeClass(rel, t.H)
	for _, allowed := range AdoptableClasses() {
		if c == allowed {
			return c, true
		}
	}
	return "", false
}

// nativeClass maps a path inside a harness config root back to its class — the
// inverse of retarget. A harness declares where a class lives natively
// (Codex commands in "prompts/"), so the reverse mapping is derived from the
// same declaration rather than hard-coded per harness.
//
// A nil harness falls back to weft's own staged layout, which is the only
// reasonable assumption for an adapter weft does not know.
func nativeClass(rel string, h Harness) Class {
	if h == nil {
		return stagedClass(rel)
	}
	head, _, found := strings.Cut(filepath.ToSlash(rel), "/")
	if !found {
		return stagedClass(rel)
	}
	for _, c := range Classes() {
		cs := classSupportOf(h, c)
		if cs.Placement != PlacementNative {
			continue
		}
		if head == nativeDirFor(c, cs) {
			return c
		}
	}
	return ClassOther
}

// nativeDirFor is the directory name a class occupies in a harness root. An
// empty SubDir means "unchanged from the staged layout", where the directory
// name is the class name itself (commands/, agents/, skills/).
func nativeDirFor(c Class, cs ClassSupport) string {
	if cs.SubDir != "" {
		return cs.SubDir
	}
	return string(c)
}

// SourceLayout names the destination directory for each adoptable class inside
// one source root. It mirrors source.Structure without importing it, so the
// harness package stays free of a dependency on the source registry.
type SourceLayout struct {
	Name     string // source name, for messages
	Root     string // absolute source root
	Commands string
	Agents   string
	Skills   string
}

// dirFor returns the destination subdirectory for c, falling back to the class
// name when the source does not configure one.
func (l SourceLayout) dirFor(c Class) string {
	var dir string
	switch c {
	case ClassCommands:
		dir = l.Commands
	case ClassAgents:
		dir = l.Agents
	case ClassSkills:
		dir = l.Skills
	case ClassInstructions, ClassMCP, ClassOther:
		dir = ""
	}
	if dir = strings.Trim(strings.TrimSpace(dir), "/"); dir == "" {
		return string(c)
	}
	return dir
}

// AdoptRequest describes one adoption of one or more files from one harness
// into one source.
type AdoptRequest struct {
	Target ScanTarget
	Rels   []string // paths relative to Target.Root
	Layout SourceLayout
	Force  bool // overwrite a destination that already exists in the source
	// Confirmed records that the user approved the plan. Without it Adopt
	// refuses with ErrConfirmRequired and writes nothing.
	Confirmed bool
}

// AdoptEntry is one planned copy: harness file in, source file out.
type AdoptEntry struct {
	Rel       string // path relative to the harness root (also its manifest key)
	Class     Class
	From      string // absolute path in the harness
	To        string // absolute path in the source
	DestRel   string // path relative to the source root
	Overwrite bool   // destination already exists (only reachable with Force)
}

// PlanAdopt validates a request and returns what Adopt would do, without
// touching the filesystem. Separating plan from execution is what lets the CLI
// print an accurate preview before asking for confirmation.
func PlanAdopt(req AdoptRequest) ([]AdoptEntry, error) {
	if req.Layout.Root == "" {
		return nil, fmt.Errorf("no source root to adopt into")
	}
	entries := make([]AdoptEntry, 0, len(req.Rels))
	for _, raw := range req.Rels {
		e, err := planOne(raw, req)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func planOne(raw string, req AdoptRequest) (AdoptEntry, error) {
	rel, err := containedRel(req.Target.Root, raw)
	if err != nil {
		return AdoptEntry{}, err
	}
	if _, owned := req.Target.Owned[rel]; owned {
		return AdoptEntry{}, fmt.Errorf("%s is already managed by weft — edit it in its source, or use write-back", rel)
	}
	if !strings.EqualFold(filepath.Ext(rel), adoptableExt) {
		return AdoptEntry{}, fmt.Errorf("%s is not adoptable — only %s files are", rel, adoptableExt)
	}
	from := filepath.Join(req.Target.Root, rel)
	info, err := os.Stat(from)
	if err != nil {
		return AdoptEntry{}, fmt.Errorf("reading %s: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		return AdoptEntry{}, fmt.Errorf("%s is not a regular file", rel)
	}

	c := nativeClass(rel, req.Target.H)
	if !isAdoptable(c) {
		return AdoptEntry{}, fmt.Errorf(
			"%s has no adoptable class in %s — weft adopts %s",
			rel, req.Target.Harness, classList(AdoptableClasses()))
	}

	data, err := os.ReadFile(from) //nolint:gosec // from is confined to the harness root by containedRel
	if err != nil {
		return AdoptEntry{}, fmt.Errorf("reading %s: %w", rel, err)
	}
	if err := checkNoSecrets(rel, data); err != nil {
		return AdoptEntry{}, err
	}

	destRel := filepath.Join(req.Layout.dirFor(c), classTail(rel))
	to := filepath.Join(req.Layout.Root, destRel)
	overwrite := false
	if _, statErr := os.Stat(to); statErr == nil {
		if !req.Force {
			return AdoptEntry{}, fmt.Errorf(
				"%s already exists in source %q (%s) — pass --force to overwrite it",
				destRel, req.Layout.Name, to)
		}
		overwrite = true
	}
	return AdoptEntry{Rel: rel, Class: c, From: from, To: to, DestRel: destRel, Overwrite: overwrite}, nil
}

// Adopt copies the planned files into the source and records weft's ownership of
// the harness copy in the manifest.
//
// Recording ownership is what makes adoption complete rather than merely a copy:
// without it the next apply would see the harness file as an external edit,
// back it up and rewrite it. With it, the file weft stages from the source is
// byte-identical to what is already on disk, so the apply reports "unchanged".
func Adopt(req AdoptRequest) ([]AdoptEntry, error) {
	entries, err := PlanAdopt(req)
	if err != nil {
		return nil, err
	}
	if !req.Confirmed {
		return entries, ErrConfirmRequired
	}
	if len(entries) == 0 {
		return entries, nil
	}

	m, err := manifest.Load(req.Target.CfgDir, req.Target.Harness)
	if err != nil {
		return nil, fmt.Errorf("loading manifest: %w", err)
	}
	for _, e := range entries {
		data, rdErr := os.ReadFile(e.From) //nolint:gosec // e.From was confined to the harness root at plan time
		if rdErr != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Rel, rdErr)
		}
		if mkErr := os.MkdirAll(filepath.Dir(e.To), 0o755); mkErr != nil {
			return nil, fmt.Errorf("creating %s: %w", filepath.Dir(e.DestRel), mkErr)
		}
		if wErr := os.WriteFile(e.To, data, 0o644); wErr != nil { //nolint:gosec // e.To is under the registered source root
			return nil, fmt.Errorf("writing %s: %w", e.DestRel, wErr)
		}
		m.Files[e.Rel] = manifest.HashBytes(data)
	}
	m.Harness = req.Target.Harness
	if m.TargetRoot == "" {
		m.TargetRoot = req.Target.Root
	}
	if err := manifest.Save(req.Target.CfgDir, m); err != nil {
		return nil, fmt.Errorf("recording ownership: %w", err)
	}
	return entries, nil
}

// containedRel cleans a user-supplied path and verifies it stays inside root.
// Absolute paths under root are accepted and relativised, which is what users
// get from shell completion. Anything that escapes is rejected outright — weft
// has a prior path-traversal incident (#84) and this is the same shape of input.
func containedRel(root, raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return "", fmt.Errorf("%s is not inside %s", raw, root)
		}
		p = rel
	}
	p = filepath.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s escapes the harness root %s", raw, root)
	}
	return p, nil
}

// classTail strips the leading class directory from a harness-relative path, so
// "prompts/review.md" adopted from Codex lands in the source's commands dir as
// "review.md" rather than "prompts/review.md".
func classTail(rel string) string {
	_, tail, found := strings.Cut(filepath.ToSlash(rel), "/")
	if !found {
		return rel
	}
	return filepath.FromSlash(tail)
}

func isAdoptable(c Class) bool {
	for _, allowed := range AdoptableClasses() {
		if c == allowed {
			return true
		}
	}
	return false
}

func classList(cs []Class) string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = string(c)
	}
	return strings.Join(names, ", ")
}

// secretDelimiters splits a line into candidate tokens. Credentials appear in
// markdown wrapped in quotes, after an "=" or ":", or inside backticks, so those
// have to be cut away before the value can be recognised.
func secretDelimiters(r rune) bool {
	switch r {
	case ' ', '\t', '"', '\'', '`', '=', ':', ',', ';', '(', ')', '[', ']', '{', '}', '<', '>', '|':
		return true
	}
	return false
}

// checkNoSecrets refuses a file carrying what looks like a literal credential.
//
// This is a security boundary, not a lint: sources are ordinary git repos that
// users push to remotes, so a token adopted into one is a token published. The
// guard is not bypassable by --force, and the error names the line so the fix is
// obvious.
func checkNoSecrets(rel string, data []byte) error {
	for i, line := range strings.Split(string(data), "\n") {
		for _, tok := range strings.FieldsFunc(line, secretDelimiters) {
			if mcpconfig.LooksSecret(tok) {
				return fmt.Errorf(
					"%s line %d looks like a literal credential (%s…) — sources are pushable, so weft will not adopt it; remove the secret or reference it as ${env:NAME}",
					rel, i+1, redact(tok))
			}
		}
	}
	return nil
}

// redactPrefixLen is how much of a suspected token is echoed back: enough for the
// user to find it in the file, too little to be useful if the message is pasted
// into an issue.
const redactPrefixLen = 6

func redact(tok string) string {
	if len(tok) <= redactPrefixLen {
		return tok
	}
	return tok[:redactPrefixLen]
}
