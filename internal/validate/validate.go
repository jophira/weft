package validate

import (
	"bytes"
	"strings"
)

// DefaultWarnSizeKB is the default threshold (in KB) above which a merged
// instruction file is considered large enough to risk reduced model compliance.
const DefaultWarnSizeKB = 96

// Result holds validation findings for a merged instruction file.
type Result struct {
	Bytes           int
	SizeWarning     bool
	DuplicateBlocks []string // short preview of each duplicated paragraph
}

// HasWarnings reports whether any warnings were found.
func (r Result) HasWarnings() bool {
	return r.SizeWarning || len(r.DuplicateBlocks) > 0
}

// Instruction validates merged instruction file content and returns findings.
// warnSizeKB is the threshold in KB; pass DefaultWarnSizeKB when no user
// override is configured.
func Instruction(content []byte, warnSizeKB int) Result {
	r := Result{Bytes: len(content)}
	r.SizeWarning = len(content) > warnSizeKB*1024
	r.DuplicateBlocks = duplicateBlocks(content)
	return r
}

// duplicateBlocks returns a preview string for each paragraph that appears
// more than once (after normalisation). Each duplicate is reported once.
func duplicateBlocks(content []byte) []string {
	seen := make(map[string]bool)
	reported := make(map[string]bool)
	var dupes []string

	for _, block := range splitBlocks(content) {
		norm := normalizeBlock(block)
		if norm == "" {
			continue
		}
		if seen[norm] && !reported[norm] {
			dupes = append(dupes, blockPreview(norm))
			reported[norm] = true
		}
		seen[norm] = true
	}
	return dupes
}

// splitBlocks splits content into paragraphs (blank-line separated).
// Each returned string has leading/trailing whitespace trimmed per line.
func splitBlocks(content []byte) []string {
	var blocks []string
	var cur strings.Builder

	for line := range bytes.SplitSeq(content, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			if cur.Len() > 0 {
				blocks = append(blocks, cur.String())
				cur.Reset()
			}
			continue
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.Write(trimmed)
	}
	if cur.Len() > 0 {
		blocks = append(blocks, cur.String())
	}
	return blocks
}

// normalizeBlock lowercases and collapses all whitespace for comparison.
func normalizeBlock(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// blockPreview returns the first 72 characters of a normalised block.
func blockPreview(s string) string {
	if len(s) > 72 {
		return s[:72] + "..."
	}
	return s
}
