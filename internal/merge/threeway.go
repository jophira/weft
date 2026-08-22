package merge

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// Three-way merge of a conflict's sides against the text weft last projected.
//
// A conflict is two or more harnesses editing the same text since the last
// apply. `--take` settles that by discarding one side; most of the time nothing
// needed discarding, because the two edits were in different places. This finds
// the genuine overlaps and leaves everything else intact.
//
// The base is on disk: for an instruction section it is the staged per-source
// copy under profiles/<profile>/instructions/, for a file it is the staged tree.
// Both hold what weft last wrote, which is exactly what a three-way merge needs.
//
// A clean merge is not a safe merge. Two harnesses can each add a rule, in
// different places, that contradict each other — the text merges without a
// marker and the model then reads both. That is why callers put the result in
// front of a human before it reaches a source or a harness.

// Side is one harness's version of the text being merged. Label names the
// harness, because "ours" and "theirs" have no third form and three harnesses
// can diverge on one file.
type Side struct {
	Label string
	Text  string
}

// Conflict markers are git's, so an editor's existing highlighting and every
// user's existing habit both apply. The labels are harness names rather than
// HEAD and a branch: neither side is "ours" here.
const (
	markerBegin  = "<<<<<<< "
	markerMiddle = "======="
	markerEnd    = ">>>>>>> "
)

// HasConflictMarkers reports whether s carries merge markers. Callers use it as
// the guard on the one thing that must never happen: marker text reaching a
// harness path, where it is read as live model input on the next turn.
func HasConflictMarkers(s string) bool {
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(line, markerBegin) ||
			line == markerMiddle ||
			strings.HasPrefix(line, markerEnd) {
			return true
		}
	}
	return false
}

// ThreeWay merges every side against base, keeping non-overlapping edits from
// all of them and marking the overlaps. conflicted reports whether any markers
// were written.
//
// More than two sides fold left: sides[0] merges with sides[1], the result
// merges with sides[2], and so on. Markers from an earlier round survive into
// the later ones as ordinary text, so an overlap between three harnesses is
// still visible as three labelled blocks.
func ThreeWay(base string, sides []Side) (result string, conflicted bool) {
	switch len(sides) {
	case 0:
		return base, false
	case 1:
		return sides[0].Text, false
	}
	acc := sides[0]
	for _, s := range sides[1:] {
		merged, c := merge3(base, acc, s)
		conflicted = conflicted || c
		acc = Side{Label: acc.Label + "+" + s.Label, Text: merged}
	}
	return acc.Text, conflicted
}

// merge3 is the diff3 core: line-diff each side against the base, group the
// changed regions that touch, and take the one side that moved — or emit both
// with markers when they both moved and disagree.
func merge3(base string, ours, theirs Side) (string, bool) {
	// Every input is given a final newline first. Without it, a file whose last
	// line has no newline shares no line with a side that added one, so the diff
	// replaces the whole last line and two edits nowhere near each other come out
	// as an overlap. The newline is the honest form of the text either way.
	baseLines := splitLines(withNewline(base))
	ourLines := splitLines(withNewline(ours.Text))
	theirLines := splitLines(withNewline(theirs.Text))

	all := append(changedRegions(baseLines, ourLines, 0), changedRegions(baseLines, theirLines, 1)...)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].oStart != all[j].oStart {
			return all[i].oStart < all[j].oStart
		}
		return all[i].side < all[j].side
	})

	sideLines := [2][]string{ourLines, theirLines}
	var b strings.Builder
	conflicted := false
	pos := 0

	for i := 0; i < len(all); {
		h := all[i]
		if h.oEnd <= pos && h.oStart < pos {
			i++ // absorbed by a region already emitted
			continue
		}
		if h.oStart > pos {
			b.WriteString(join(baseLines[pos:h.oStart]))
		}
		regStart, regEnd := h.oStart, h.oEnd
		group := []region{h}
		j := i + 1
		for j < len(all) && belongsInRegion(all[j], regEnd) {
			if all[j].oEnd > regEnd {
				regEnd = all[j].oEnd
			}
			group = append(group, all[j])
			j++
		}

		baseText := join(baseLines[regStart:regEnd])
		ourText := regionText(group, 0, baseLines, sideLines[0], regStart, regEnd)
		theirText := regionText(group, 1, baseLines, sideLines[1], regStart, regEnd)

		switch {
		case ourText == theirText, theirText == baseText:
			b.WriteString(ourText)
		case ourText == baseText:
			b.WriteString(theirText)
		default:
			conflicted = true
			writeConflict(&b, ours.Label, ourText, theirs.Label, theirText)
		}
		pos = regEnd
		i = j
	}
	b.WriteString(join(baseLines[pos:]))
	return b.String(), conflicted
}

// belongsInRegion reports whether a hunk has to be settled together with the
// region accumulated so far.
//
// Touching counts, not just overlapping, which is diff3's rule and the one
// `git merge-file` implements: two changes with no unchanged base line between
// them are one conflict, even though their line ranges do not intersect. One
// harness rewriting base line 1 while another rewrites line 2 is therefore
// reported rather than combined.
//
// That is deliberately the conservative reading of "non-overlapping". Nothing in
// the text says which of two abutting rewrites comes first, and the same rule is
// what makes two insertions at the same point — whose ranges are both empty and
// so can never intersect — come out as a conflict instead of being silently
// concatenated in whatever order the sides happened to sort. Every user's
// intuition here is calibrated on git, and the review step means a marker costs
// a moment in an editor the user already has open.
func belongsInRegion(h region, regEnd int) bool {
	return h.oStart <= regEnd
}

// region is one side's changed span, expressed in both base and side line
// numbers so the region text can be recovered from either.
type region struct {
	side         int // 0 = ours, 1 = theirs
	oStart, oEnd int // half-open range of base lines
	nStart, nEnd int // half-open range of that side's lines
}

// changedRegions line-diffs base against other and returns the spans where they
// differ, with consecutive delete/insert runs collapsed into one span.
func changedRegions(baseLines, otherLines []string, side int) []region {
	dmp := diffmatchpatch.New()
	// DiffLinesToChars maps each distinct line to one rune, so DiffMain runs on
	// lines rather than characters and the rune counts below are line counts.
	// The decoded text is never needed: the line slices are already to hand.
	a, bText, _ := dmp.DiffLinesToChars(join(baseLines), join(otherLines))
	diffs := dmp.DiffMain(a, bText, false)

	var out []region
	bi, oi := 0, 0
	for i := 0; i < len(diffs); {
		if diffs[i].Type == diffmatchpatch.DiffEqual {
			n := utf8.RuneCountInString(diffs[i].Text)
			bi, oi = bi+n, oi+n
			i++
			continue
		}
		bs, os := bi, oi
		for i < len(diffs) && diffs[i].Type != diffmatchpatch.DiffEqual {
			n := utf8.RuneCountInString(diffs[i].Text)
			if diffs[i].Type == diffmatchpatch.DiffDelete {
				bi += n
			} else {
				oi += n
			}
			i++
		}
		out = append(out, region{side: side, oStart: bs, oEnd: bi, nStart: os, nEnd: oi})
	}
	return out
}

// regionText renders what one side holds across the base range [regStart,
// regEnd). A side with no change in the group holds the base text verbatim;
// otherwise its own lines replace the span its regions cover, with the
// untouched base lines on either edge kept.
func regionText(group []region, side int, baseLines, sideLines []string, regStart, regEnd int) string {
	first, last := -1, -1
	for idx, r := range group {
		if r.side != side {
			continue
		}
		if first < 0 {
			first = idx
		}
		last = idx
	}
	if first < 0 {
		return join(baseLines[regStart:regEnd])
	}
	return join(baseLines[regStart:group[first].oStart]) +
		join(sideLines[group[first].nStart:group[last].nEnd]) +
		join(baseLines[group[last].oEnd:regEnd])
}

func writeConflict(b *strings.Builder, ourLabel, ourText, theirLabel, theirText string) {
	b.WriteString(markerBegin)
	b.WriteString(ourLabel)
	b.WriteByte('\n')
	b.WriteString(withNewline(ourText))
	b.WriteString(markerMiddle)
	b.WriteByte('\n')
	b.WriteString(withNewline(theirText))
	b.WriteString(markerEnd)
	b.WriteString(theirLabel)
	b.WriteByte('\n')
}

func withNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// splitLines splits s into lines that keep their trailing newline, so joining
// them reproduces s byte for byte. This is the same split
// diffmatchpatch.DiffLinesToChars performs, which is what keeps the rune counts
// in changedRegions aligned with these indices.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return append(out, s)
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
		if s == "" {
			return out
		}
	}
}

func join(lines []string) string { return strings.Join(lines, "") }
