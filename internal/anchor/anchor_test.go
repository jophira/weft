package anchor

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestExpandRoot(t *testing.T) {
	in := []byte("see @{{weft.root}}/common/code-review.md and {{weft.root}}/java/x.md")
	got := string(Expand(in, Anchors{Root: "/home/me/rules/work"}))
	want := "see @/home/me/rules/work/common/code-review.md and /home/me/rules/work/java/x.md"
	if got != want {
		t.Errorf("Expand root =\n  %q\nwant\n  %q", got, want)
	}
}

func TestExpandHomeAndDocs(t *testing.T) {
	in := []byte("kb at {{weft.home}}/work and adr at {{weft.docs}}/weft/adr")
	got := string(Expand(in, Anchors{Home: "/home/me/weft", Docs: "/home/me/docs"}))
	want := "kb at /home/me/weft/work and adr at /home/me/docs/weft/adr"
	if got != want {
		t.Errorf("Expand home/docs =\n  %q\nwant\n  %q", got, want)
	}
}

func TestExpandEmptyHomeDocsLeavesTokens(t *testing.T) {
	in := []byte("{{weft.home}} {{weft.docs}}")
	if got := string(Expand(in, Anchors{})); got != "{{weft.home}} {{weft.docs}}" {
		t.Errorf("empty Home/Docs should leave tokens, got %q", got)
	}
}

func TestExpandNamedSource(t *testing.T) {
	byName := map[string]string{"team": "/srv/team", "me": "/home/me/rules"}
	in := []byte("{{weft.source:team}}/x.md and {{weft.source:me}}/y.md")
	got := string(Expand(in, Anchors{Root: "/self", ByName: byName}))
	want := "/srv/team/x.md and /home/me/rules/y.md"
	if got != want {
		t.Errorf("Expand named =\n  %q\nwant\n  %q", got, want)
	}
}

func TestExpandUnknownNamedLeftVisible(t *testing.T) {
	in := []byte("{{weft.source:ghost}}/x.md")
	got := string(Expand(in, Anchors{Root: "/self", ByName: map[string]string{"team": "/srv/team"}}))
	if got != "{{weft.source:ghost}}/x.md" {
		t.Errorf("unknown named source should be left untouched, got %q", got)
	}
}

func TestExpandEmptySelfRootLeavesRootToken(t *testing.T) {
	in := []byte("{{weft.root}}/x.md")
	if got := string(Expand(in, Anchors{})); got != "{{weft.root}}/x.md" {
		t.Errorf("empty selfRoot should leave root token, got %q", got)
	}
}

func TestExpandNoTokensIsIdentity(t *testing.T) {
	in := []byte("plain content, no anchors")
	got := Expand(in, Anchors{Root: "/self"})
	if string(got) != string(in) {
		t.Errorf("no-token content changed: %q", got)
	}
}

// roundTripCases is shared between the explicit round-trip tests and the
// property test below: every case must satisfy Collapse(Expand(x, a), a) == x.
var roundTripCases = []struct {
	name string
	in   string
	a    Anchors
}{
	{
		name: "root",
		in:   "see @{{weft.root}}/common/code-review.md and {{weft.root}}/java/x.md",
		a:    Anchors{Root: "/home/me/rules/work"},
	},
	{
		name: "home and docs",
		in:   "kb at {{weft.home}}/work and adr at {{weft.docs}}/weft/adr",
		a:    Anchors{Home: "/home/me/weft", Docs: "/home/me/docs"},
	},
	{
		name: "named source",
		in:   "{{weft.source:team}}/x.md and {{weft.source:me}}/y.md",
		a: Anchors{
			Root:   "/self",
			ByName: map[string]string{"team": "/srv/team", "me": "/home/me/rules"},
		},
	},
	{
		name: "docs nested under home — longest match wins",
		in:   "home at {{weft.home}} and docs at {{weft.docs}}/adr/0001.md",
		a:    Anchors{Home: "/home/me/weft", Docs: "/home/me/weft/docs"},
	},
	{
		name: "one source nested under another",
		in:   "{{weft.source:outer}}/x.md and {{weft.source:inner}}/y.md",
		a: Anchors{
			ByName: map[string]string{
				"outer": "/home/me/weft/sources",
				"inner": "/home/me/weft/sources/team",
			},
		},
	},
	{
		name: "no anchors is identity",
		in:   "plain content, no anchors",
		a:    Anchors{Root: "/self"},
	},
}

func TestRoundTrip(t *testing.T) {
	for _, c := range roundTripCases {
		t.Run(c.name, func(t *testing.T) {
			expanded := Expand([]byte(c.in), c.a)
			got := string(Collapse(expanded, c.a))
			if got != c.in {
				t.Errorf("Collapse(Expand(x)) =\n  %q\nwant\n  %q\n(expanded: %q)", got, c.in, expanded)
			}
		})
	}
}

// TestRoundTripProperty is the property test the issue asks for:
// Collapse(Expand(x, a), a) == x for every anchor form, exercised across
// randomly combined tokens rather than one fixed string per form.
func TestRoundTripProperty(t *testing.T) {
	forms := []struct {
		token string
		a     Anchors
	}{
		{RootToken, Anchors{Root: "/r/oot"}},
		{HomeToken, Anchors{Home: "/h/ome"}},
		{DocsToken, Anchors{Docs: "/d/ocs"}},
		{"{{weft.source:team}}", Anchors{ByName: map[string]string{"team": "/s/rc/team"}}},
	}
	merged := Anchors{
		Root: "/r/oot", Home: "/h/ome", Docs: "/d/ocs",
		ByName: map[string]string{"team": "/s/rc/team"},
	}
	rnd := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic seed for a reproducible property test, not security-sensitive
	for iter := 0; iter < 100; iter++ {
		n := rnd.Intn(4) + 1
		var b strings.Builder
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(" and ")
			}
			b.WriteString(forms[rnd.Intn(len(forms))].token)
			fmt.Fprintf(&b, "/segment-%d.md", rnd.Intn(1000))
		}
		in := b.String()
		got := string(Collapse(Expand([]byte(in), merged), merged))
		if got != in {
			t.Fatalf("property failed on iteration %d: Collapse(Expand(%q)) = %q", iter, in, got)
		}
	}
}

func TestCollapseSamePathPrefersRoot(t *testing.T) {
	// A source registered at exactly {{weft.root}}'s own path: the self-form
	// should win over naming the source explicitly.
	a := Anchors{Root: "/home/me/work", ByName: map[string]string{"self": "/home/me/work"}}
	in := []byte("/home/me/work/x.md")
	got := string(Collapse(in, a))
	if got != "{{weft.root}}/x.md" {
		t.Errorf("same-path collision should prefer {{weft.root}}, got %q", got)
	}
}

func TestCollapseNoMatchLeavesPathUntouched(t *testing.T) {
	a := Anchors{Root: "/home/me/work", Home: "/home/me/weft"}
	in := []byte("/etc/hosts and /home/me/other/x.md")
	got := string(Collapse(in, a))
	if got != string(in) {
		t.Errorf("unmatched path should be left untouched, got %q", got)
	}
}

func TestCollapsePrefersDifferentSourceOverRoot(t *testing.T) {
	// A path belonging to a different, registered source must collapse to
	// that source's own token, not to {{weft.root}} (which names the source
	// currently being written, not the one the path actually points at).
	a := Anchors{
		Root:   "/home/me/personal",
		ByName: map[string]string{"team": "/srv/team", "personal": "/home/me/personal"},
	}
	in := []byte("/srv/team/x.md")
	got := string(Collapse(in, a))
	if got != "{{weft.source:team}}/x.md" {
		t.Errorf("path belonging to another source should collapse to its own token, got %q", got)
	}
}

func TestHas(t *testing.T) {
	cases := map[string]bool{
		"{{weft.root}}":     true,
		"{{weft.home}}":     true,
		"{{weft.docs}}":     true,
		"{{weft.source:x}}": true,
		"nothing here":      false,
		"{{weft.unknown}}":  false,
		"a {{weft.root}} b": true,
	}
	for in, want := range cases {
		if got := Has([]byte(in)); got != want {
			t.Errorf("Has(%q) = %v, want %v", in, got, want)
		}
	}
}
