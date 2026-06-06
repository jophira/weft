package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/merge"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
	"github.com/jophira/weft/internal/watch"
)

// writeBackMergedSource handles write-back for target files assembled from multiple
// sources (AppendStrategy / OverlayMerge). It re-assembles the baseline from the
// current source files, diffs it against the edited target, and routes changed line
// regions back to their owning source files.
//
// Returns (true, nil) when at least one source was updated.
// Returns (false, nil) when called for a single-source file or when there is nothing to do.
func writeBackMergedSource(
	m *manifest.Manifest,
	c watch.TargetChange,
	p *profile.Profile,
	srcs []source.Source,
) (bool, error) {
	sourceNames := m.SourceFiles[c.Rel]
	if len(sourceNames) <= 1 {
		return false, nil
	}

	srcMap := make(map[string]source.Source, len(srcs))
	for _, s := range srcs {
		srcMap[s.Name] = s
	}

	// Read each contributing source's current file content (in manifest order).
	bodies := make([][]byte, len(sourceNames))
	for i, name := range sourceNames {
		s, ok := srcMap[name]
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Root, c.Rel))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("reading source %s/%s: %w", name, c.Rel, err)
		}
		bodies[i] = data
	}

	baseline := rebuildMerged(bodies)

	edited, err := os.ReadFile(filepath.Join(c.Root, c.Rel))
	if err != nil {
		return false, fmt.Errorf("reading target %s: %w", c.Rel, err)
	}
	if bytes.Equal(baseline, edited) {
		return false, nil
	}

	bounds := mergedLineBoundaries(bodies)
	baselineLines := splitLines(string(baseline))
	editedLines := splitLines(string(edited))
	script := lcsEditScript(baselineLines, editedLines)
	sourceNewLines := attributeLinesToSources(script, bounds, len(sourceNames), editedLines)

	performed := false
	for i, name := range sourceNames {
		newLines := sourceNewLines[i]
		original := bodies[i]

		endsWithNL := len(original) > 0 && original[len(original)-1] == '\n'
		newContent := strings.Join(newLines, "\n")
		if endsWithNL && len(newLines) > 0 {
			newContent += "\n"
		}

		if newContent == string(original) {
			continue
		}

		s, ok := srcMap[name]
		if !ok {
			continue
		}
		dst := filepath.Join(s.Root, c.Rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return false, fmt.Errorf("creating dir for %s: %w", dst, err)
		}
		if err := os.WriteFile(dst, []byte(newContent), 0o644); err != nil { //nolint:gosec // dst derived from source root config, not user input
			return false, fmt.Errorf("writing %s to source %s: %w", c.Rel, name, err)
		}
		performed = true
	}
	return performed, nil
}

// rebuildMerged re-assembles the merged file content from ordered source bodies
// using AppendStrategy, mirroring what MergeRoots does for OverlayMerge profiles.
func rebuildMerged(bodies [][]byte) []byte {
	var result []byte
	for _, body := range bodies {
		if len(body) == 0 {
			continue
		}
		result, _ = merge.AppendStrategy(result, body)
	}
	return result
}

// mergedLineBoundaries returns the [startLine, endLine] inclusive 0-indexed line
// ranges for each source body within the assembled merged output. Empty/nil bodies
// return [-1, -1].
func mergedLineBoundaries(bodies [][]byte) [][2]int {
	bounds := make([][2]int, len(bodies))
	currentLine := 0
	endsWithNL := true // treat empty-so-far as ending with newline

	for i, body := range bodies {
		if len(body) == 0 {
			bounds[i] = [2]int{-1, -1}
			continue
		}
		if !endsWithNL {
			// AppendStrategy inserts a separator '\n', which closes the previous
			// partial line and advances to a new one.
			currentLine++
		}
		startLine := currentLine
		parts := strings.Split(string(body), "\n")
		if parts[len(parts)-1] == "" {
			// body ends with '\n': trailing element is empty, not a real line.
			lineCount := len(parts) - 1
			bounds[i] = [2]int{startLine, startLine + lineCount - 1}
			currentLine = startLine + lineCount
			endsWithNL = true
		} else {
			// body doesn't end with '\n': last element is a partial line.
			lineCount := len(parts)
			bounds[i] = [2]int{startLine, startLine + lineCount - 1}
			currentLine = startLine + lineCount - 1
			endsWithNL = false
		}
	}
	return bounds
}

// splitLines splits s into individual line strings without trailing newlines.
// Both "a\nb\n" and "a\nb" produce ["a", "b"]. Empty string returns nil.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// editOp is one step in the shortest edit script that transforms baseline into edited.
type editOp struct {
	kind    byte // 'e'=equal, 'd'=delete, 'i'=insert
	baseIdx int  // index in baseline ('e' and 'd')
	editIdx int  // index in edited  ('e' and 'i')
}

// lcsEditScript computes the shortest edit script transforming baseline into edited
// using an O(M*N) LCS DP, suitable for the small files typical of AI-rules content.
func lcsEditScript(baseline, edited []string) []editOp {
	m, n := len(baseline), len(edited)

	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			switch {
			case baseline[i-1] == edited[j-1]:
				dp[i][j] = dp[i-1][j-1] + 1
			case dp[i-1][j] >= dp[i][j-1]:
				dp[i][j] = dp[i-1][j]
			default:
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Trace back from (m, n) to (0, 0) to build the edit script in reverse.
	ops := make([]editOp, 0, m+n)
	i, j := m, n
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && baseline[i-1] == edited[j-1]:
			ops = append(ops, editOp{'e', i - 1, j - 1})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			// edited[j-1] inserted before baseline[i]
			ops = append(ops, editOp{'i', i, j - 1})
			j--
		default:
			// baseline[i-1] deleted
			ops = append(ops, editOp{'d', i - 1, j})
			i--
		}
	}

	// Reverse to get forward order.
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}

// attributeLinesToSources walks the edit script and assigns each edited line to the
// source that owns the corresponding baseline line. Consecutive delete/insert ops
// are grouped into replace hunks and paired (deleted[k] ↔ inserted[k]) so that a
// replaced line is attributed to the source that owned the deleted line, not just
// the generic insertion point. Unmatched inserts go to the preceding source.
//
// Returns the new content lines for each source (same order as bounds/sourceNames).
func attributeLinesToSources(script []editOp, bounds [][2]int, numSources int, editedLines []string) [][]string {
	result := make([][]string, numSources)
	for i := range result {
		result[i] = []string{}
	}

	sourceFor := func(baseLine int) int {
		for i, b := range bounds {
			if b[0] >= 0 && b[0] <= baseLine && baseLine <= b[1] {
				return i
			}
		}
		return -1
	}

	firstSource := func() int {
		for i, b := range bounds {
			if b[0] >= 0 {
				return i
			}
		}
		return 0
	}

	lastSource := func() int {
		for i := len(bounds) - 1; i >= 0; i-- {
			if bounds[i][0] >= 0 {
				return i
			}
		}
		return numSources - 1
	}

	i := 0
	for i < len(script) {
		op := script[i]
		if op.kind == 'e' {
			src := sourceFor(op.baseIdx)
			if src >= 0 {
				result[src] = append(result[src], editedLines[op.editIdx])
			}
			i++
			continue
		}

		// Collect a replace hunk: all consecutive non-equal ops.
		var deleted, inserted []int
		insertPos := op.baseIdx
		for i < len(script) && script[i].kind != 'e' {
			cur := script[i]
			if cur.kind == 'd' {
				deleted = append(deleted, cur.baseIdx)
			} else {
				inserted = append(inserted, cur.editIdx)
				insertPos = cur.baseIdx
			}
			i++
		}

		// Pair each inserted line with the source owning the corresponding deleted line.
		n := len(deleted)
		if len(inserted) < n {
			n = len(inserted)
		}
		for k := 0; k < n; k++ {
			src := sourceFor(deleted[k])
			if src >= 0 {
				result[src] = append(result[src], editedLines[inserted[k]])
			}
		}
		// Unmatched inserts (more inserts than deletes) go to the last deleted source,
		// or the preceding source for pure insertions.
		for k := n; k < len(inserted); k++ {
			var src int
			switch {
			case len(deleted) > 0:
				src = sourceFor(deleted[len(deleted)-1])
			case insertPos == 0:
				src = firstSource()
			default:
				src = sourceFor(insertPos - 1)
				if src < 0 {
					src = lastSource()
				}
			}
			if src >= 0 {
				result[src] = append(result[src], editedLines[inserted[k]])
			}
		}
		// Extra deletes (more deletes than inserts) are dropped — those lines are gone.
	}
	return result
}
