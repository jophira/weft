package rules

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// Evaluator decides whether a rule's Detect predicate matches a repo Context.
// It is an interface so the predicate language is swappable: the default
// implementation uses CEL, but a structured-matcher implementation could be
// dropped in without touching the resolver.
type Evaluator interface {
	// Eval reports whether detect is true for ctx. An empty detect is always
	// false (dependency-only rule). A predicate that fails to compile or does
	// not yield a boolean returns a non-nil error so the resolver can record it
	// as skipped rather than silently treating it as a non-match.
	Eval(detect string, ctx Context) (bool, error)
}

// CEL variable names exposed to detect predicates.
const (
	varFiles  = "files"
	varDeps   = "deps"
	varRepo   = "repo"
	varRemote = "remote"
)

// hasFileFunc is the CEL function name for nested-manifest globbing. It is not
// spelled has() — CEL reserves has() as a built-in macro (has(msg.field)) and
// the parser rejects any user function of that exact name — so the glob helper
// takes the collision-free, self-documenting name hasFile(glob).
const hasFileFunc = "hasFile"

// celEvaluator evaluates detect predicates with google/cel-go. Compiled
// programs are cached by source string, since the same predicate is evaluated
// once per rule and may recur across resolves.
//
// The hasFile() function needs the repo root to glob against, but CEL function
// bindings are fixed at env-build time and cannot read the per-call activation.
// So the current root is stashed on the evaluator under mu for the duration of
// each Eval, and the hasFile() binding reads it back. The whole of Eval —
// stashing the root and running the program — runs under mu, so concurrent
// resolves with different roots cannot interleave.
type celEvaluator struct {
	env   *cel.Env
	mu    sync.Mutex
	cache map[string]cel.Program
	// root is the repo root of the Context currently being evaluated; set at the
	// top of each Eval and read by the hasFile() binding. Empty when unset.
	root string
	// globCache memoises the relative file list of a root so repeated hasFile()
	// calls (across rules in one resolve) walk the tree at most once per root.
	globCache map[string][]string
}

// NewCELEvaluator builds an Evaluator whose predicates may reference the
// `files`, `deps`, `repo` and `remote` variables of a Context, plus the
// hasFile(glob) function for nested-manifest detection.
func NewCELEvaluator() (Evaluator, error) {
	e := &celEvaluator{cache: make(map[string]cel.Program), globCache: make(map[string][]string)}
	env, err := cel.NewEnv(
		cel.Variable(varFiles, cel.ListType(cel.StringType)),
		cel.Variable(varDeps, cel.ListType(cel.StringType)),
		cel.Variable(varRepo, cel.StringType),
		cel.Variable(varRemote, cel.StringType),
		cel.Function(hasFileFunc,
			cel.Overload("has_file_string", []*cel.Type{cel.StringType}, cel.BoolType,
				cel.UnaryBinding(func(arg ref.Val) ref.Val {
					pattern, ok := arg.Value().(string)
					if !ok {
						return types.Bool(false)
					}
					return types.Bool(e.hasPath(pattern))
				}),
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("building CEL environment: %w", err)
	}
	e.env = env
	return e, nil
}

func (e *celEvaluator) Eval(detect string, ctx Context) (bool, error) {
	if detect == "" {
		return false, nil
	}
	// Hold the lock across the whole evaluation: the hasFile() binding reads e.root,
	// which we stash here, so no other Eval may run concurrently with a
	// different root. (cf. Java: a synchronized method guarding shared state.)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.root = ctx.Root
	prg, err := e.programLocked(detect)
	if err != nil {
		return false, err
	}
	out, _, err := prg.Eval(map[string]any{
		varFiles:  ctx.Files,
		varDeps:   ctx.Deps,
		varRepo:   ctx.Repo,
		varRemote: ctx.Remote,
	})
	if err != nil {
		return false, fmt.Errorf("evaluating predicate %q: %w", detect, err)
	}
	matched, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("predicate %q did not evaluate to a boolean", detect)
	}
	return matched, nil
}

// programLocked compiles and caches the CEL program for src, verifying it is a
// boolean expression. Callers must hold e.mu.
func (e *celEvaluator) programLocked(src string) (cel.Program, error) {
	if prg, ok := e.cache[src]; ok {
		return prg, nil
	}
	ast, iss := e.env.Compile(src)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("compiling predicate %q: %w", src, iss.Err())
	}
	if ast.OutputType() != cel.BoolType {
		return nil, fmt.Errorf("predicate %q must be boolean, got %s", src, ast.OutputType())
	}
	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("building program for %q: %w", src, err)
	}
	e.cache[src] = prg
	return prg, nil
}

// hasPath reports whether any file under the current repo root matches the
// doublestar glob pattern. Paths are matched relative to the root with '/'
// separators (so "**/pom.xml" matches "pom.xml" and "svc/api/pom.xml"). It is
// total: an empty root, an unreadable tree, or an invalid pattern yields false.
// Callers reach it only via the hasFile() binding, which runs under e.mu.
func (e *celEvaluator) hasPath(pattern string) bool {
	if pattern == "" || e.root == "" {
		return false
	}
	for _, rel := range e.filesForLocked(e.root) {
		if ok, err := doublestar.Match(pattern, rel); ok && err == nil {
			return true
		}
	}
	return false
}

// filesForLocked returns the root-relative, slash-separated paths of every
// regular file under root, skipping hidden directories (.git, .idea, ...) as
// loadTree does. The walk runs at most once per root and is memoised, so a
// predicate set with several hasFile() calls pays the tree cost a single time.
// Callers must hold e.mu.
func (e *celEvaluator) filesForLocked(root string) []string {
	if cached, ok := e.globCache[root]; ok {
		return cached
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // degrade: skip unreadable entries rather than fail detection
		}
		if d.IsDir() {
			if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(root, path); err == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	e.globCache[root] = files
	return files
}
