// Package testutil holds small fixture builders shared by tests across
// packages that would otherwise duplicate them independently (e.g. the
// in-process cmd integration tests and the spawned-binary e2e tests).
package testutil

import "strings"

// RuleFile renders a rule file with resolver front-matter. detect is wrapped
// in double quotes so CEL predicates containing single quotes ('pom.xml' in
// files) survive. detect "" makes the rule dependency-only.
func RuleFile(label, detect, body string, extends ...string) string {
	var b strings.Builder
	b.WriteString("---\nlabel: " + label + "\ndetect: \"" + detect + "\"\n")
	if len(extends) > 0 {
		b.WriteString("extends: [" + strings.Join(extends, ", ") + "]\n")
	}
	b.WriteString("---\n\n" + body + "\n")
	return b.String()
}
