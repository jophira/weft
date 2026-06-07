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
// sources. The routing strategy depends on the profile's overlay mode:
//
//   - OverlayCascade: the target is the last (winning) source's content, so the
//     edited file is written directly to the last source in sourceNames. No LCS
//     attribution is performed.
//   - All other overlays (OverlayMerge, etc.): re-assembles the baseline via
//     AppendStrategy, diffs it against the edited target, and routes changed line
//     regions back to their owning source files using LCS attribution.
//
// Returns (true, nil) when at least one source was updated.
// Returns (false, nil) when called for a single-source file or when there is nothing to do.
func writeBackMergedSource(
	m *manifest.Manifest,
	c watch.TargetChange,
	p *profile.Profile,
	srcs []source.Source,
) (bool, error) {
	return writeBackMergedSourceMap(m, c, p, buildSrcMap(srcs))
}

// writeBackMergedSourceMap is the map-accepting variant of writeBackMergedSource.
// Use this in batch loops where the srcMap has already been built once.
func writeBackMergedSourceMap(
	m *manifest.Manifest,
	c watch.TargetChange,
	p *profile.Profile,
	srcMap map[string]source.Source,
) (bool, error) {
	sourceNames := m.SourceFiles[c.Rel]
	if len(sourceNames) <= 1 {
		return false, nil
	}

	// For cascade overlay the merged target is the last source's content verbatim.
	// Write the edited target directly to the cascade winner (last source).
	if p.Overlay == profile.OverlayCascade {
		return writeBackCascadeWinner(c, sourceNames, srcMap)
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

	baselineLines := splitLines(string(baseline))
	editedLines := splitLines(string(edited))

	// Size guard: skip the O(M×N) LCS DP for very large files and fall back to
	// the cascade-winner strategy. maxLinesForLCS keeps the direction table within
	// a reasonable heap budget (5000×5000 bytes = ~25 MB worst case).
	if len(baselineLines) > maxLinesForLCS || len(editedLines) > maxLinesForLCS {
		return writeBackCascadeWinner(c, sourceNames, srcMap)
	}

	bounds := mergedLineBoundaries(bodies)
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

// writeBackCascadeWinner writes the edited target content to the last (winning)
// source in sourceNames — the cascade winner. It reads the edited target and
// compares it to the winner's current content to avoid spurious writes.
func writeBackCascadeWinner(
	c watch.TargetChange,
	sourceNames []string,
	srcMap map[string]source.Source,
) (bool, error) {
	// Find the last source that is present in srcMap (the cascade winner).
	winnerName := ""
	for i := len(sourceNames) - 1; i >= 0; i-- {
		if _, ok := srcMap[sourceNames[i]]; ok {
			winnerName = sourceNames[i]
			break
		}
	}
	if winnerName == "" {
		return false, nil
	}

	edited, err := os.ReadFile(filepath.Join(c.Root, c.Rel))
	if err != nil {
		return false, fmt.Errorf("reading target %s: %w", c.Rel, err)
	}

	winner := srcMap[winnerName]
	dst := filepath.Join(winner.Root, c.Rel)

	// Avoid a write if the winner already has the same content.
	existing, err := os.ReadFile(dst)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("reading winner source %s/%s: %w", winnerName, c.Rel, err)
	}
	if bytes.Equal(existing, edited) {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, fmt.Errorf("creating dir for %s: %w", dst, err)
	}
	if err := os.WriteFile(dst, edited, 0o644); err != nil { //nolint:gosec // dst derived from source root config, not user input
		return false, fmt.Errorf("writing %s to source %s: %w", c.Rel, winnerName, err)
	}
	return true, nil
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

// maxLinesForLCS is the per-input size limit for the LCS DP path. Files larger
// than this threshold fall back to writeBackCascadeWinner to avoid the O(M×N)
// allocation cost on very large inputs.
const maxLinesForLCS = 5000

// lcsDir encodes the traceback direction stored per DP cell.
// Using a byte table (1 byte/cell) instead of an int table (8 bytes/cell on
// 64-bit) reduces the working-set size by 8× for the normal path.
// cf. Java: byte[] vs int[] — same trade-off, narrower type = less heap pressure.
//
// Tiebreaker: when dp[i-1][j] == dp[i][j-1] the traceback prefers Left (insert),
// so lcsDirLeft is stored when curr[j-1] >= prev[j] (i.e. left >= up). This
// matches the original traceback condition `dp[i][j-1] >= dp[i-1][j]`.
const (
	lcsDirEqual byte = 'e' // diagonal: baseline[i-1] == edited[j-1]
	lcsDirUp    byte = 'u' // up:       dp[i-1][j] > dp[i][j-1]  (strict)
	lcsDirLeft  byte = 'l' // left:     dp[i][j-1] >= dp[i-1][j] (with ties)
)

// lcsEditScript computes the shortest edit script transforming baseline into edited.
//
// For inputs within maxLinesForLCS the algorithm uses:
//   - Rolling 2-row DP (prev/curr) — only two []int rows live at a time instead
//     of a full (m+1)×(n+1) int table; saves O(M×N) int allocations.
//     cf. Python: swap via tuple unpacking; cf. Java: manual tmp variable needed.
//   - A separate (m+1)×(n+1) byte direction table for traceback — 8× smaller
//     than storing full int values, and allocated as a single flat slice.
//
// For inputs exceeding maxLinesForLCS the caller must fall back (this function
// panics if called beyond the limit — callers are responsible for the guard).
func lcsEditScript(baseline, edited []string) []editOp {
	m, n := len(baseline), len(edited)

	// Rolling DP: two rows of length (n+1) — the DP value is only needed for
	// the current and previous row during the fill pass.
	prev := make([]int, n+1)
	curr := make([]int, n+1)

	// Direction table: one byte per cell, stored as a flat slice to avoid the
	// overhead of a slice-of-slices allocation.
	// cf. Java: new byte[m+1][n+1] — Go uses a 1D backing array with manual indexing.
	dir := make([]byte, (m+1)*(n+1))
	idx := func(i, j int) int { return i*(n+1) + j }

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			switch {
			case baseline[i-1] == edited[j-1]:
				curr[j] = prev[j-1] + 1
				dir[idx(i, j)] = lcsDirEqual
			case curr[j-1] >= prev[j]:
				// Traceback prefers Left (insert) on ties — store Left when
				// dp[i][j-1] >= dp[i-1][j], matching the original traceback condition.
				curr[j] = curr[j-1]
				dir[idx(i, j)] = lcsDirLeft
			default:
				curr[j] = prev[j]
				dir[idx(i, j)] = lcsDirUp
			}
		}
		// Swap rows: reuse the allocation, discard prev.
		// cf. Python: prev, curr = curr, prev
		prev, curr = curr, prev
		// Reset curr for the next iteration (prev now holds the row we just filled).
		for j := range curr {
			curr[j] = 0
		}
	}

	// Trace back from (m, n) to (0, 0) using the direction table.
	ops := make([]editOp, 0, m+n)
	i, j := m, n
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && dir[idx(i, j)] == lcsDirEqual:
			ops = append(ops, editOp{'e', i - 1, j - 1})
			i--
			j--
		case j > 0 && (i == 0 || dir[idx(i, j)] == lcsDirLeft):
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
		paired := min(len(deleted), len(inserted))
		for k := range paired {
			src := sourceFor(deleted[k])
			if src >= 0 {
				result[src] = append(result[src], editedLines[inserted[k]])
			}
		}
		// Unmatched inserts (more inserts than deletes) go to the last deleted source,
		// or the preceding source for pure insertions.
		for k := paired; k < len(inserted); k++ {
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
