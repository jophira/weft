package harness

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Class is a kind of projected file, distinguished by how a harness consumes it.
//
// Weft used to route files by exact relative path via a rename map, which only
// ever matched the root instruction file. Everything else kept its staged path,
// so a command staged as "commands/review.md" landed at ~/.codex/commands/review.md
// — a directory Codex never reads. Classifying by kind lets each harness say where
// a class lives natively, or that it has no native home at all.
type Class string

const (
	ClassInstructions Class = "instructions" // root instruction file (CLAUDE.md / AGENTS.md / …)
	ClassCommands     Class = "commands"     // slash-command / prompt templates
	ClassAgents       Class = "agents"       // subagent persona definitions
	ClassSkills       Class = "skills"       // progressive-disclosure skill bundles
	ClassMCP          Class = "mcp"          // MCP server definitions
	ClassOther        Class = "other"        // anything weft does not classify
)

// Classes lists every class weft routes, in a stable order suitable for logs
// and for validating user config.
func Classes() []Class {
	return []Class{ClassInstructions, ClassCommands, ClassAgents, ClassSkills, ClassMCP, ClassOther}
}

// ParseClass converts a config string to a Class, reporting whether it is known.
func ParseClass(s string) (Class, bool) {
	for _, c := range Classes() {
		if string(c) == strings.ToLower(strings.TrimSpace(s)) {
			return c, true
		}
	}
	return "", false
}

// Placement says where a class lives inside a harness's config root, and how it
// is consumed.
type Placement int

const (
	// PlacementNone: the harness has no native home for this class. Weft must not
	// write the files anywhere — copying them to a path the harness ignores is the
	// bug this model exists to fix.
	PlacementNone Placement = iota
	// PlacementNative: the harness executes this class from a known subdirectory.
	PlacementNative
	// PlacementInstruction: the class is folded into the harness's root instruction
	// file rather than existing as separate files (currently only ClassInstructions).
	PlacementInstruction
)

// ClassSupport describes one harness's handling of one class.
type ClassSupport struct {
	Placement Placement
	// SubDir is the class's directory relative to the harness config root, used
	// when Placement is PlacementNative. Empty means "keep the staged path
	// unchanged" — the right default for harnesses whose layout weft does not
	// know, since inventing a relocation would be worse than leaving it alone.
	SubDir string
	// Advertise requests that unsupported files still be surfaced to the harness
	// as a read-on-demand index in its instruction file (ADR 0004 D9). Only
	// meaningful when Placement is PlacementNone and the content is prose.
	Advertise bool
}

// Supported reports whether the harness writes this class's files natively.
func (cs ClassSupport) Supported() bool { return cs.Placement != PlacementNone }

// ClassAware is an optional Harness extension declaring per-class handling.
// Harnesses that do not implement it fall back to defaultClassSupport, which
// preserves the historical directory-copy behaviour for unknown tools.
type ClassAware interface {
	ClassSupport(c Class) ClassSupport
}

// stagedClass infers a file's class from its staged relative path.
// The staged tree is weft's own layout (mirroring Claude Code's), so this is a
// structural mapping, not a guess about the target harness.
//
// cf. Java: a switch over the first path segment; Go has no pattern matching on
// paths, so the segment is extracted explicitly.
func stagedClass(rel string) Class {
	slash := filepath.ToSlash(rel)
	if !strings.Contains(slash, "/") {
		// Root-level file. CLAUDE.md is the instruction file; anything else
		// (README.md, stray dotfiles) is unclassified.
		if strings.EqualFold(slash, instructionFileName) {
			return ClassInstructions
		}
		return ClassOther
	}
	switch head, _, _ := strings.Cut(slash, "/"); head {
	case "commands":
		return ClassCommands
	case "agents":
		return ClassAgents
	case "skills":
		return ClassSkills
	default:
		return ClassOther
	}
}

// instructionFileName is the staged name of the root instruction file. Harnesses
// rename it on projection (AGENTS.md for Codex, weft.mdc for Cursor).
const instructionFileName = "CLAUDE.md"

// classSupportOf resolves a harness's support for a class, applying the
// permissive default for harnesses that predate the class model.
func classSupportOf(h Harness, c Class) ClassSupport {
	if ca, ok := h.(ClassAware); ok {
		return ca.ClassSupport(c)
	}
	return defaultClassSupport(c)
}

// defaultClassSupport is the fallback for harnesses that do not declare class
// handling — notably GenericHarness and user-defined entries from
// harnesses.yaml, where weft knows nothing about the tool's conventions.
//
// It copies every class at its staged path, which is exactly the pre-class-model
// behaviour. That is wrong for tools with their own layout, but weft has no basis
// to guess a better one, and silently dropping files would be a worse default
// than placing them where the user can find and relocate them.
func defaultClassSupport(c Class) ClassSupport {
	if c == ClassInstructions {
		return ClassSupport{Placement: PlacementInstruction}
	}
	return ClassSupport{Placement: PlacementNative, SubDir: ""}
}

// routeStaged resolves where a staged file lands in the target, or reports that
// it must not be written at all.
//
// An explicit rename always wins: it is how the root instruction file gets its
// per-harness name (CLAUDE.md → AGENTS.md), which predates the class model.
// Otherwise the file's class decides, and a class with no native home yields
// ok=false rather than a path the harness would ignore.
//
// A nil harness keeps the historical copy-everything behaviour, so callers that
// have no Harness value (tests, ad-hoc applies) are unaffected.
func routeStaged(rel string, renames map[string]string, h Harness) (string, bool) {
	if renamed, ok := renames[rel]; ok {
		return renamed, true
	}
	if h == nil {
		return rel, true
	}
	c := stagedClass(rel)
	return retarget(rel, c, classSupportOf(h, c))
}

// reportSkipped logs one line per class whose files were not written, so a
// harness silently gaining or losing a capability is visible in the apply output
// rather than inferred from a missing directory.
func reportSkipped(out io.Writer, skipped map[Class]int, h Harness) {
	if len(skipped) == 0 {
		return
	}
	for _, c := range Classes() { // stable order
		n, ok := skipped[c]
		if !ok || n == 0 {
			continue
		}
		reason := "no native location in this harness"
		if h != nil && classSupportOf(h, c).Advertise {
			reason = "advertised in the instruction index instead"
		}
		fmt.Fprintf(out, "  ~ %-9s %d %s file(s) — %s\n", statusSkipped, n, c, reason)
	}
}

// retarget maps a staged relative path to its path inside the harness config
// root, given that class's support. Returns ok=false when the class has no
// native home, meaning the file must not be written.
//
// The staged path's first segment is the class directory in weft's own layout;
// it is replaced by the harness's SubDir. "commands/review.md" with SubDir
// "prompts" becomes "prompts/review.md".
func retarget(rel string, c Class, cs ClassSupport) (string, bool) {
	if !cs.Supported() {
		return "", false
	}
	slash := filepath.ToSlash(rel)
	if cs.SubDir == "" || c == ClassOther || !strings.Contains(slash, "/") {
		return rel, true // no relocation requested, unclassified, or root-level
	}
	_, tail, _ := strings.Cut(slash, "/")
	return filepath.FromSlash(cs.SubDir + "/" + tail), true
}
