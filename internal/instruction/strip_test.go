package instruction

import (
	"strings"
	"testing"
)

func TestStrip_removesBlockKeepsAuthoredContent(t *testing.T) {
	authored := "# Mine\n\nkeep this\n"
	withBlock := Upsert([]byte(authored), "weft body")

	got := string(Strip(withBlock))
	if strings.Contains(got, "weft body") {
		t.Errorf("Strip left weft's block behind: %q", got)
	}
	if !strings.Contains(got, "keep this") {
		t.Errorf("Strip removed the user's content: %q", got)
	}
}

func TestStrip_isTheInverseOfUpsert(t *testing.T) {
	authored := "# Mine\n\nkeep this\n"
	round := string(Strip(Upsert([]byte(authored), "weft body")))
	if round != authored {
		t.Errorf("Strip(Upsert(x)) = %q, want %q", round, authored)
	}
}

func TestStrip_contentWithNoBlockIsUnchanged(t *testing.T) {
	in := "# Just mine\n\nnothing managed here\n"
	if got := string(Strip([]byte(in))); got != in {
		t.Errorf("Strip = %q, want the input unchanged", got)
	}
}

func TestStrip_blockOnlyYieldsNothing(t *testing.T) {
	// A file weft created entirely: stripping it leaves no user content, which
	// callers treat as "this file contributes nothing".
	only := Upsert(nil, "weft body")
	if got := Strip(only); len(got) != 0 {
		t.Errorf("Strip = %q, want empty for a weft-only file", got)
	}
}

func TestStrip_keepsContentOnBothSidesOfTheBlock(t *testing.T) {
	s := "before\n\n" + BlockBegin + "\nbody\n" + BlockEnd + "\n\nafter\n"
	got := string(Strip([]byte(s)))
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("Strip = %q, want both sides preserved", got)
	}
	if strings.Contains(got, "body") {
		t.Errorf("Strip = %q, want the block gone", got)
	}
}
