package anchor

import "testing"

func TestExpandRoot(t *testing.T) {
	in := []byte("see @{{weft.root}}/common/code-review.md and {{weft.root}}/java/x.md")
	got := string(Expand(in, "/home/me/rules/work", nil))
	want := "see @/home/me/rules/work/common/code-review.md and /home/me/rules/work/java/x.md"
	if got != want {
		t.Errorf("Expand root =\n  %q\nwant\n  %q", got, want)
	}
}

func TestExpandNamedSource(t *testing.T) {
	byName := map[string]string{"team": "/srv/team", "me": "/home/me/rules"}
	in := []byte("{{weft.source:team}}/x.md and {{weft.source:me}}/y.md")
	got := string(Expand(in, "/self", byName))
	want := "/srv/team/x.md and /home/me/rules/y.md"
	if got != want {
		t.Errorf("Expand named =\n  %q\nwant\n  %q", got, want)
	}
}

func TestExpandUnknownNamedLeftVisible(t *testing.T) {
	in := []byte("{{weft.source:ghost}}/x.md")
	got := string(Expand(in, "/self", map[string]string{"team": "/srv/team"}))
	if got != "{{weft.source:ghost}}/x.md" {
		t.Errorf("unknown named source should be left untouched, got %q", got)
	}
}

func TestExpandEmptySelfRootLeavesRootToken(t *testing.T) {
	in := []byte("{{weft.root}}/x.md")
	if got := string(Expand(in, "", nil)); got != "{{weft.root}}/x.md" {
		t.Errorf("empty selfRoot should leave root token, got %q", got)
	}
}

func TestExpandNoTokensIsIdentity(t *testing.T) {
	in := []byte("plain content, no anchors")
	got := Expand(in, "/self", nil)
	if string(got) != string(in) {
		t.Errorf("no-token content changed: %q", got)
	}
}

func TestHas(t *testing.T) {
	cases := map[string]bool{
		"{{weft.root}}":     true,
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
