// Package instruction builds and edits the weft-managed block that weft writes
// into a harness's root instruction file (CLAUDE.md, AGENTS.md, …).
//
// Two projection strategies share one managed-block envelope:
//
//   - import (Tier A): the block holds import directives pointing at weft's own
//     per-source instruction copies. The harness (Claude Code, Gemini CLI) follows
//     the imports. Content stays out of the harness's global file.
//   - inline (Tier B): the block holds the per-source content concatenated with
//     attribution markers. The only mechanism single-file harnesses support; also
//     the safe default for unknown harnesses.
//
// In both cases everything outside the managed block is preserved byte-for-byte,
// so a user's own notes in the file are never touched.
package instruction

import (
	"strconv"
	"strings"
)

// Managed-block envelope markers. Distinct from the content-level
// weft:sources / weft:projects placeholders and the weft:source attribution
// markers, so the three never collide.
const (
	BlockBegin = "<!-- weft:begin — managed by weft; edit your sources, not this block -->"
	BlockEnd   = "<!-- weft:end -->"

	// managedNote heads the block body as a human reminder.
	importNote = "<!-- weft imports your sources in priority order; later entries win on conflict -->"
	inlineNote = "<!-- weft assembled your sources in priority order; later entries win on conflict -->"
)

// SourceContent pairs a source name with its assembled instruction text, used to
// build an inline (Tier B) block with per-source attribution.
type SourceContent struct {
	Name    string
	Content string
}

// ImportBody builds the body of an import (Tier A) managed block. Each path is
// rendered through template, where the literal "{path}" is replaced by the path.
// Paths must already be formatted for the harness (typically forward-slashed
// absolute paths). Order is preserved: callers pass low→high priority so the
// highest-priority import lands last.
func ImportBody(paths []string, template string) string {
	var b strings.Builder
	b.WriteString(importNote)
	for _, p := range paths {
		b.WriteByte('\n')
		b.WriteString(strings.ReplaceAll(template, "{path}", p))
	}
	return b.String()
}

// InlineBody builds the body of an inline (Tier B) managed block: each source's
// content wrapped in attribution markers, concatenated in the given order
// (low→high priority). Empty sources are skipped. The attribution markers match
// the format the write-back path expects, so the block can be split back into
// per-source files.
func InlineBody(sources []SourceContent) string {
	var b strings.Builder
	b.WriteString(inlineNote)
	for _, s := range sources {
		if strings.TrimSpace(s.Content) == "" {
			continue // nothing meaningful to contribute
		}
		content := strings.Trim(s.Content, "\n")
		b.WriteByte('\n')
		b.WriteString(attributionBegin(s.Name))
		b.WriteByte('\n')
		b.WriteString(content)
		b.WriteByte('\n')
		b.WriteString(attributionEnd(s.Name))
	}
	return b.String()
}

// attributionBegin/End render the per-source markers. The name is quoted so it
// survives names containing spaces or punctuation.
func attributionBegin(name string) string {
	return "<!-- weft:source:begin name=" + strconv.Quote(name) + " -->"
}

func attributionEnd(name string) string {
	return "<!-- weft:source:end name=" + strconv.Quote(name) + " -->"
}

// Upsert returns existing with the managed block inserted or replaced by one
// wrapping body, preserving all content outside the block byte-for-byte.
//
//   - No existing block: the block is appended, separated from prior content by
//     a blank line. Empty input yields just the block.
//   - Existing block: its body is replaced in place; surrounding content is kept.
//
// The returned content always ends with a single trailing newline.
func Upsert(existing []byte, body string) []byte {
	block := BlockBegin + "\n" + body + "\n" + BlockEnd
	s := string(existing)

	if start, end, ok := blockSpan(s); ok {
		// Replace the existing block (from BlockBegin to end of BlockEnd line).
		return []byte(ensureTrailingNewline(s[:start] + block + s[end:]))
	}

	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		return []byte(ensureTrailingNewline(block))
	}
	return []byte(ensureTrailingNewline(trimmed + "\n\n" + block))
}

// Extract returns the body of the managed block in content, or ("", false) when
// no block is present. Leading/trailing newlines around the body are trimmed.
func Extract(content []byte) (body string, found bool) {
	s := string(content)
	start, end, ok := blockSpan(s)
	if !ok {
		return "", false
	}
	inner := s[start+len(BlockBegin) : end]
	// inner spans from just after BlockBegin to the start of BlockEnd line; it
	// includes the BlockEnd marker's leading newline + the marker itself only if
	// blockSpan included it, so strip the trailing BlockEnd here.
	inner = strings.TrimSuffix(strings.TrimRight(inner, "\n"), BlockEnd)
	return strings.Trim(inner, "\n"), true
}

// blockSpan locates the managed block within s. It returns the byte offset of
// BlockBegin and the offset just past BlockEnd (end of that line). ok is false
// when either marker is missing or out of order.
func blockSpan(s string) (start, end int, ok bool) {
	start = strings.Index(s, BlockBegin)
	if start < 0 {
		return 0, 0, false
	}
	endMarker := strings.Index(s[start:], BlockEnd)
	if endMarker < 0 {
		return 0, 0, false
	}
	end = start + endMarker + len(BlockEnd)
	return start, end, true
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
