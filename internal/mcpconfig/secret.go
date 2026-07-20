package mcpconfig

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
)

// Thresholds for the generic high-entropy check. Short strings are excluded
// outright because ordinary values ("npx", "-y", "true", a package name) sit
// well under the length bar, and letting them through would make the guard cry
// wolf often enough that users learn to ignore it.
const (
	minSecretLen     = 20  // below this, entropy is not evidence of anything
	minSecretEntropy = 3.5 // bits per character, Shannon over the value's own alphabet
)

// secretPrefixes are token shapes that are unambiguous on sight: no legitimate
// MCP env value starts with one. Checked before the entropy heuristic so a
// short-but-obvious token ("sk-abc") is still caught.
var secretPrefixes = []string{
	"sk-ant-",     // Anthropic
	"sk-",         // OpenAI and lookalikes
	"ghp_",        // GitHub personal access token (classic)
	"gho_",        // GitHub OAuth token
	"github_pat_", // GitHub fine-grained PAT
	"AKIA",        // AWS access key id
	"xoxb-",       // Slack bot token
	"xoxp-",       // Slack user token
	"AIza",        // Google API key
}

// Indirection forms weft accepts in canonical env and header values. All three
// are anchored: a value must be *entirely* a reference, because a value like
// "Bearer ${env:TOKEN}" would still need shell-style expansion that the target
// harnesses do not all perform.
var indirectionForms = []*regexp.Regexp{
	regexp.MustCompile(`^\$\{env:[A-Za-z_][A-Za-z0-9_]*\}$`), // ${env:GITHUB_TOKEN} — weft's preferred form
	regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`),     // ${GITHUB_TOKEN}
	regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`),         // $GITHUB_TOKEN
}

// secretAlphabet matches strings drawn entirely from the base64/hex character
// set that encoded credentials use. Anything containing a space, a colon, a
// quote or similar is structured data (a URL, a sentence, a JSON blob) rather
// than an opaque token, and is left alone.
var secretAlphabet = regexp.MustCompile(`^[A-Za-z0-9+/=_.-]+$`)

// pathLike matches values that are filesystem paths. Paths clear both the
// length and alphabet bars and can clear the entropy bar too, and MCP servers
// legitimately take them (a filesystem server's allowed root, for instance), so
// they are excluded explicitly rather than left to the heuristic.
var pathLike = regexp.MustCompile(`^(~|\.{1,2})?/|^[A-Za-z]:[\\/]`)

// IsIndirection reports whether v references a value held elsewhere rather than
// carrying it inline. These are the only env and header values weft will store
// in a source.
func IsIndirection(v string) bool {
	return slices.ContainsFunc(indirectionForms, func(re *regexp.Regexp) bool {
		return re.MatchString(v)
	})
}

// LooksSecret reports whether v appears to be a literal credential.
//
// It is a heuristic and deliberately biased towards false negatives: a missed
// token is caught by the reviewer, whereas a false positive blocks a legitimate
// config and teaches the user to work around the guard. Two independent signals
// are used — a known vendor prefix, or an opaque high-entropy string.
func LooksSecret(v string) bool {
	if v == "" || IsIndirection(v) {
		return false
	}
	for _, p := range secretPrefixes {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	if len(v) < minSecretLen || pathLike.MatchString(v) || !secretAlphabet.MatchString(v) {
		return false
	}
	return shannonEntropy(v) >= minSecretEntropy
}

// shannonEntropy returns the per-character entropy of s in bits, measured over
// the characters s actually uses. Random tokens approach log2(alphabet size)
// — roughly 6 bits for base64 — while English words and package names, whose
// letters repeat, land near 3.
func shannonEntropy(s string) float64 {
	counts := map[rune]int{}
	total := 0
	for _, r := range s {
		counts[r]++
		total++
	}
	var bits float64
	for _, n := range counts {
		p := float64(n) / float64(total)
		bits -= p * math.Log2(p)
	}
	return bits
}

// Validate checks c for structural errors and, most importantly, for literal
// credentials in env and header values.
//
// Servers are checked in sorted name order so a config with several problems
// always reports the same one first — repeatable output matters when the error
// is surfaced by a watcher tick rather than an interactive command.
func (c Config) Validate() error {
	for _, name := range slices.Sorted(maps.Keys(c.Servers)) {
		if err := c.Servers[name].validate(name); err != nil {
			return err
		}
	}
	return nil
}

func (s Server) validate(name string) error {
	// The transport decides which fields carry meaning, and the dialects encode
	// only those. Rejecting a mixed entry here keeps that from becoming silent
	// truncation on the way to a native file.
	switch s.Type {
	case "", TypeStdio:
		if s.Command == "" {
			return fmt.Errorf("mcp server %q: stdio servers need a command", name)
		}
		if s.URL != "" || len(s.Headers) > 0 {
			return fmt.Errorf("mcp server %q: stdio servers cannot set url or headers", name)
		}
	case TypeHTTP, TypeSSE:
		if s.URL == "" {
			return fmt.Errorf("mcp server %q: %s servers need a url", name, s.Type)
		}
		if s.Command != "" || len(s.Args) > 0 || len(s.Env) > 0 {
			return fmt.Errorf("mcp server %q: %s servers cannot set command, args or env", name, s.Type)
		}
	default:
		return fmt.Errorf("mcp server %q: unknown type %q — use %q, %q or %q", name, s.Type, TypeStdio, TypeHTTP, TypeSSE)
	}
	for _, key := range slices.Sorted(maps.Keys(s.Env)) {
		if LooksSecret(s.Env[key]) {
			return secretError(name, "env", key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(s.Headers)) {
		if LooksSecret(s.Headers[key]) {
			return secretError(name, "header", key)
		}
	}
	return nil
}

// secretError explains the fix, not just the fault: the user needs to know what
// to put in the file instead and that the real value has to live in their shell.
func secretError(server, kind, key string) error {
	return fmt.Errorf("mcp server %q: %s %s holds a literal credential — replace it with %q and export the value in your shell",
		server, kind, key, "${env:"+envVarName(key)+"}")
}

// envVarName turns a config key into the shell variable name suggested in the
// error, so a header key like "Authorization" or "X-Api-Key" yields a usable
// AUTHORIZATION / X_API_KEY rather than something the user has to invent.
func envVarName(key string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(key) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
