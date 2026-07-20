package cmd

import (
	"fmt"
	"strings"

	"github.com/jophira/weft/internal/harness"
	"github.com/jophira/weft/internal/profile"
)

// allowedClasses converts a profile's harness_sync entry for one target into the
// class set applyWithManifest and ProjectInstruction filter on.
//
// Returning a nil map means unrestricted — every class the harness natively
// supports. That is the default for a harness with no entry, so profiles written
// before harness_sync existed keep projecting exactly what they did before.
// An explicit empty list yields an empty non-nil map, which projects nothing.
func allowedClasses(p *profile.Profile, target string) (map[harness.Class]bool, error) {
	names, configured := p.HarnessSync.ClassesFor(target)
	if !configured {
		return nil, nil
	}
	allowed := make(map[harness.Class]bool, len(names))
	for _, name := range names {
		c, ok := harness.ParseClass(name)
		if !ok {
			return nil, fmt.Errorf(
				"profile %q: harness_sync.%s lists unknown class %q — valid classes are %s",
				p.Name, target, name, classNameList())
		}
		allowed[c] = true
	}
	return allowed, nil
}

// classNameList renders the valid class names for error messages.
func classNameList() string {
	names := make([]string, 0, len(harness.Classes()))
	for _, c := range harness.Classes() {
		names = append(names, string(c))
	}
	return strings.Join(names, ", ")
}
