package instruction

import (
	"strings"
	"testing"
)

func TestImportBody_ordersAndTemplates(t *testing.T) {
	body := ImportBody([]string{"/w/10-personal.md", "/w/20-team.md"}, "@{path}")
	lines := strings.Split(body, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected note + 2 imports, got %d lines: %q", len(lines), body)
	}
	if lines[1] != "@/w/10-personal.md" || lines[2] != "@/w/20-team.md" {
		t.Errorf("import lines wrong/out of order: %q", body)
	}
}

func TestInlineBody_wrapsAttributionAndSkipsEmpty(t *testing.T) {
	body := InlineBody([]SourceContent{
		{Name: "personal", Content: "p-rules"},
		{Name: "empty", Content: "  \n  "},
		{Name: "team", Content: "t-rules"},
	})
	if strings.Contains(body, "empty") {
		t.Errorf("empty source should be skipped:\n%s", body)
	}
	wantOrder := []string{
		`<!-- weft:source:begin name="personal" -->`,
		"p-rules",
		`<!-- weft:source:end name="personal" -->`,
		`<!-- weft:source:begin name="team" -->`,
		"t-rules",
		`<!-- weft:source:end name="team" -->`,
	}
	last := -1
	for _, frag := range wantOrder {
		idx := strings.Index(body, frag)
		if idx < 0 {
			t.Fatalf("missing %q in:\n%s", frag, body)
		}
		if idx < last {
			t.Errorf("fragment %q out of order in:\n%s", frag, body)
		}
		last = idx
	}
}

func TestUpsert_emptyInputYieldsBlockOnly(t *testing.T) {
	got := string(Upsert(nil, "BODY"))
	want := BlockBegin + "\nBODY\n" + BlockEnd + "\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpsert_appendsAfterUserContent(t *testing.T) {
	existing := []byte("# My own notes\n\nkeep me\n")
	got := string(Upsert(existing, "BODY"))
	if !strings.HasPrefix(got, "# My own notes\n\nkeep me\n") {
		t.Errorf("user content not preserved at top:\n%s", got)
	}
	if !strings.Contains(got, BlockBegin) || !strings.HasSuffix(got, BlockEnd+"\n") {
		t.Errorf("managed block not appended:\n%s", got)
	}
}

func TestUpsert_replacesInPlacePreservingOutside(t *testing.T) {
	before := "TOP CONTENT\n\n"
	after := "\n\nBOTTOM CONTENT\n"
	existing := []byte(before + BlockBegin + "\nOLD\n" + BlockEnd + after)

	got := string(Upsert(existing, "NEW"))

	if !strings.HasPrefix(got, "TOP CONTENT") {
		t.Errorf("top content lost:\n%s", got)
	}
	if !strings.Contains(got, "BOTTOM CONTENT") {
		t.Errorf("bottom content lost:\n%s", got)
	}
	if strings.Contains(got, "OLD") {
		t.Errorf("old block body not replaced:\n%s", got)
	}
	if !strings.Contains(got, "\nNEW\n") {
		t.Errorf("new block body missing:\n%s", got)
	}
}

func TestUpsert_idempotent(t *testing.T) {
	once := Upsert([]byte("user stuff\n"), "BODY")
	twice := Upsert(once, "BODY")
	if string(once) != string(twice) {
		t.Errorf("Upsert not idempotent:\nonce:  %q\ntwice: %q", once, twice)
	}
}

func TestExtract_roundTrip(t *testing.T) {
	body := InlineBody([]SourceContent{{Name: "personal", Content: "p-rules"}})
	file := Upsert([]byte("outside\n"), body)

	got, found := Extract(file)
	if !found {
		t.Fatal("expected to find managed block")
	}
	if got != body {
		t.Errorf("extracted body mismatch:\ngot:  %q\nwant: %q", got, body)
	}
}

func TestExtract_notFound(t *testing.T) {
	if _, found := Extract([]byte("no block here\n")); found {
		t.Error("expected found=false for content without a managed block")
	}
}
