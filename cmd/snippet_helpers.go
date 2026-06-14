package cmd

import "strings"

// buildNameSet converts a slice of directory names into a lookup map, stripping
// any trailing slashes or whitespace.
func buildNameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		if n = strings.TrimRight(strings.TrimSpace(n), "/\\"); n != "" {
			set[n] = true
		}
	}
	return set
}

// replacePlaceholderBlock replaces the first begin...end block in content with
// replacement. Returns content unchanged when either marker is absent.
func replacePlaceholderBlock(content, begin, end, replacement string) string {
	start := strings.Index(content, begin)
	if start < 0 {
		return content
	}
	finish := strings.Index(content[start:], end)
	if finish < 0 {
		return content
	}
	finish += start + len(end)
	return content[:start] + replacement + content[finish:]
}
