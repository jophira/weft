// Package diff compares two staged profile directories and formats the results.
package diff

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// Kind classifies how a file differs between two staged directories.
type Kind int

const (
	Same    Kind = iota // identical content in both directories
	Added               // present only in B (would be gained switching A→B)
	Removed             // present only in A (would be lost switching A→B)
	Changed             // present in both but with different content
)

// File describes the diff status of one relative path.
type File struct {
	Rel  string
	Kind Kind
}

// Compare walks dirA and dirB and returns a sorted slice classifying every
// relative path found in either directory.
func Compare(dirA, dirB string) ([]File, error) {
	pathsA, err := listFiles(dirA)
	if err != nil {
		return nil, err
	}
	pathsB, err := listFiles(dirB)
	if err != nil {
		return nil, err
	}

	all := unionKeys(pathsA, pathsB)
	result := make([]File, 0, len(all))
	for _, rel := range all {
		_, inA := pathsA[rel]
		_, inB := pathsB[rel]
		var kind Kind
		switch {
		case inA && !inB:
			kind = Removed
		case !inA && inB:
			kind = Added
		default:
			contA, _ := os.ReadFile(filepath.Join(dirA, rel))
			contB, _ := os.ReadFile(filepath.Join(dirB, rel))
			if bytes.Equal(contA, contB) {
				kind = Same
			} else {
				kind = Changed
			}
		}
		result = append(result, File{Rel: rel, Kind: kind})
	}
	return result, nil
}

// LineDiff returns an ANSI-colored line-level diff of textA → textB, with
// removed lines in red (prefix "- "), added lines in green (prefix "+ "),
// and equal lines uncolored (prefix "  ").
func LineDiff(textA, textB string) string {
	dmp := diffmatchpatch.New()
	charsA, charsB, lineArray := dmp.DiffLinesToChars(textA, textB)
	diffs := dmp.DiffMain(charsA, charsB, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)

	var sb strings.Builder
	for _, d := range diffs {
		lines := strings.Split(d.Text, "\n")
		for i, line := range lines {
			if i == len(lines)-1 && line == "" {
				break // trailing empty string from Split when Text ends with \n
			}
			switch d.Type {
			case diffmatchpatch.DiffInsert:
				sb.WriteString(colorize(ColorCodeGreen, "+ "+line))
			case diffmatchpatch.DiffDelete:
				sb.WriteString(colorize(ColorCodeRed, "- "+line))
			case diffmatchpatch.DiffEqual:
				sb.WriteString("  ")
				sb.WriteString(line)
			}
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// ContentLines returns the full content of path with every line prefixed by
// prefix, suitable for showing added/removed files in verbose mode.
func ContentLines(path, prefix, color string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	var sb strings.Builder
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			break
		}
		sb.WriteString(colorize(color, prefix+line))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// ── ANSI helpers ──────────────────────────────────────────────────────────────

// ColorCodeRed and ColorCodeGreen are the ANSI escape sequences used by this
// package. Callers that build output outside the package (e.g. ContentLines)
// may pass these directly.
const (
	ColorCodeRed   = "\033[31m"
	ColorCodeGreen = "\033[32m"
	colorCodeReset = "\033[0m"
)

func colorize(code, s string) string {
	if code == "" {
		return s
	}
	return code + s + colorCodeReset //nolint:gocritic // appendAssign: intentional concatenation to wrap string with ANSI codes
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func listFiles(dir string) (map[string]struct{}, error) {
	paths := map[string]struct{}{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		paths[rel] = struct{}{}
		return nil
	})
	return paths, err
}

func unionKeys(a, b map[string]struct{}) []string {
	merged := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		merged[k] = struct{}{}
	}
	for k := range b {
		merged[k] = struct{}{}
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
