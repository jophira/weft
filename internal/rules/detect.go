package rules

import (
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"
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

// celEvaluator evaluates detect predicates with google/cel-go. Compiled
// programs are cached by source string, since the same predicate is evaluated
// once per rule and may recur across resolves.
type celEvaluator struct {
	env   *cel.Env
	mu    sync.Mutex
	cache map[string]cel.Program
}

// NewCELEvaluator builds an Evaluator whose predicates may reference the
// `files` and `deps` string-list variables of a Context.
func NewCELEvaluator() (Evaluator, error) {
	env, err := cel.NewEnv(
		cel.Variable(varFiles, cel.ListType(cel.StringType)),
		cel.Variable(varDeps, cel.ListType(cel.StringType)),
		cel.Variable(varRepo, cel.StringType),
		cel.Variable(varRemote, cel.StringType),
	)
	if err != nil {
		return nil, fmt.Errorf("building CEL environment: %w", err)
	}
	return &celEvaluator{env: env, cache: make(map[string]cel.Program)}, nil
}

func (e *celEvaluator) Eval(detect string, ctx Context) (bool, error) {
	if detect == "" {
		return false, nil
	}
	prg, err := e.program(detect)
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

// program compiles and caches the CEL program for src, verifying it is a
// boolean expression.
func (e *celEvaluator) program(src string) (cel.Program, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
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
