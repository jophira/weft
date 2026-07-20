package harness

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jophira/weft/internal/instruction"
	"github.com/jophira/weft/internal/manifest"
)

// Strategy is how a harness consumes weft's per-source instruction content.
type Strategy string

const (
	// StrategyImport: the harness follows import directives that point at weft's
	// own per-source copies (Claude Code, Gemini CLI). Content stays out of the
	// harness's global file.
	StrategyImport Strategy = "import"
	// StrategyInline: the harness reads a single file, so content is inlined into
	// a managed block. The safe default for any harness, including unknown ones.
	StrategyInline Strategy = "inline"
)

// defaultImportTemplate is used when an import-strategy harness specifies none.
const defaultImportTemplate = "@{path}"

// InstructionSpec describes a harness's root instruction file and how weft fills
// the managed block within it.
type InstructionSpec struct {
	Path           string   // absolute path to the root instruction file
	Strategy       Strategy // import or inline
	ImportTemplate string   // per-line template for import strategy, e.g. "@{path}"
	Preamble       string   // seeded outside the block when the file is first created (e.g. Cursor frontmatter)
}

// InstructionConsumer is implemented by harnesses that read a single root
// instruction file. Harnesses without one (e.g. Warp) do not implement it and
// are skipped by ProjectInstruction.
type InstructionConsumer interface {
	InstructionSpec() (InstructionSpec, error)
}

// SourceInstruction is one source's assembled, placeholder-expanded instruction
// text plus the absolute path of weft's own copy of it (used by import strategy).
// Callers pass these in low→high priority order so the highest-priority source
// is emitted last and wins on conflict.
type SourceInstruction struct {
	Name     string
	Content  string
	CopyPath string // abs path to weft's own copy under ~/.config/weft/.../instructions/
}

// ProjectInstruction writes weft's managed block into the harness's root
// instruction file. Import-strategy harnesses get import directives pointing at
// each source's weft-owned copy; inline-strategy harnesses get the concatenated,
// attributed content. Content outside the managed block is preserved
// byte-for-byte. A harness with no instruction file is a silent no-op.
//
// It records the block-body hash (not the whole file) in the manifest so the
// write-back path can later detect external edits to the block alone.
// stagedRoot is weft's staged tree for the active profile; it is scanned for
// classes the harness cannot execute natively but can still read on demand, which
// are appended to the block as an index (ADR 0004 D9). Pass "" to skip that scan.
func ProjectInstruction(h Harness, stagedRoot string, sources []SourceInstruction, ctx ApplyCtx) error {
	ic, ok := h.(InstructionConsumer)
	if !ok {
		return nil // harness consumes no root instruction file
	}
	spec, err := ic.InstructionSpec()
	if err != nil {
		return fmt.Errorf("resolving instruction spec for %s: %w", h.Name(), err)
	}

	body := buildBody(spec, sources)

	if stagedRoot != "" {
		entries, sErr := scanAdvertised(stagedRoot, h)
		if sErr != nil {
			return sErr
		}
		body += advertiseBody(entries)
	}

	existing, err := os.ReadFile(spec.Path)
	switch {
	case os.IsNotExist(err):
		existing = []byte(spec.Preamble) // seed preamble (if any) outside the block
	case err != nil:
		return fmt.Errorf("reading %s: %w", spec.Path, err)
	}
	updated := instruction.Upsert(existing, body)

	if mkErr := os.MkdirAll(filepath.Dir(spec.Path), 0o755); mkErr != nil {
		return fmt.Errorf("creating dir for %s: %w", spec.Path, mkErr)
	}
	if wErr := os.WriteFile(spec.Path, updated, 0o644); wErr != nil { //nolint:gosec // path resolved from harness config, not user input
		return fmt.Errorf("writing %s: %w", spec.Path, wErr)
	}

	m, err := manifest.Load(ctx.CfgDir, h.Name())
	if err != nil {
		return err
	}
	m.Harness = h.Name()
	if ctx.ProfileName != "" {
		m.Profile = ctx.ProfileName
	}
	m.InstructionPath = spec.Path
	m.InstructionBlock = manifest.HashBytes([]byte(body))
	return manifest.Save(ctx.CfgDir, m)
}

// homeJoin resolves the user's home directory and joins the given parts onto it.
func homeJoin(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

// buildBody renders the managed-block body for the harness's strategy.
func buildBody(spec InstructionSpec, sources []SourceInstruction) string {
	if spec.Strategy == StrategyImport {
		paths := make([]string, 0, len(sources))
		for _, s := range sources {
			// Forward-slash even on Windows: import directives are path-like text,
			// not OS file ops, and harnesses expect "/" separators.
			paths = append(paths, filepath.ToSlash(s.CopyPath))
		}
		tmpl := spec.ImportTemplate
		if tmpl == "" {
			tmpl = defaultImportTemplate
		}
		return instruction.ImportBody(paths, tmpl)
	}

	sc := make([]instruction.SourceContent, 0, len(sources))
	for _, s := range sources {
		sc = append(sc, instruction.SourceContent{Name: s.Name, Content: s.Content})
	}
	return instruction.InlineBody(sc)
}
