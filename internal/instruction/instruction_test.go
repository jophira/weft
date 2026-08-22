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

func TestGeneratedFileNote_isSingleLineAndNamesSource(t *testing.T) {
	note := GeneratedFileNote("personal")
	if strings.Contains(note, "\n") {
		t.Errorf("note must be a single line: %q", note)
	}
	if !strings.Contains(note, `"personal"`) {
		t.Errorf("note must name its source: %q", note)
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

func TestParseInline_roundTripsInlineBody(t *testing.T) {
	in := []SourceContent{
		{Name: "personal", Content: "p-rules\nline2"},
		{Name: "company", Content: "c-rules"},
	}
	got := ParseInline(InlineBody(in))
	if len(got) != len(in) {
		t.Fatalf("got %d sections, want %d: %+v", len(got), len(in), got)
	}
	for i := range in {
		if got[i].Name != in[i].Name || got[i].Content != in[i].Content {
			t.Errorf("section %d = %+v, want %+v", i, got[i], in[i])
		}
	}
}

func TestParseInline_ignoresContentOutsideMarkers(t *testing.T) {
	body := "stray note\n" +
		`<!-- weft:source:begin name="x" -->` + "\nXC\n" + `<!-- weft:source:end name="x" -->` + "\ntrailing"
	got := ParseInline(body)
	if len(got) != 1 || got[0].Name != "x" || got[0].Content != "XC" {
		t.Errorf("ParseInline = %+v, want one section x=XC", got)
	}
}

func TestParseInline_noMarkersYieldsNothing(t *testing.T) {
	if got := ParseInline("just some text\nno markers"); len(got) != 0 {
		t.Errorf("expected no sections, got %+v", got)
	}
}

func TestReplaceSection_swapsOneSectionAndLeavesTheRest(t *testing.T) {
	body := InlineBody([]SourceContent{
		{Name: "pers", Content: "pers rules"},
		{Name: "work", Content: "work rules"},
	})
	body += "\n<!-- an advertised-index tail -->"

	got, ok := ReplaceSection(body, "pers", "resolved pers rules")
	if !ok {
		t.Fatal("ReplaceSection did not find the section")
	}
	if !strings.Contains(got, "resolved pers rules") || strings.Contains(got, "\npers rules\n") {
		t.Errorf("section not replaced:\n%s", got)
	}
	for _, want := range []string{"work rules", "an advertised-index tail"} {
		if !strings.Contains(got, want) {
			t.Errorf("ReplaceSection dropped %q:\n%s", want, got)
		}
	}
	// The round trip still splits back into the same two sources.
	secs := ParseInline(got)
	if len(secs) != 2 || secs[0].Content != "resolved pers rules" || secs[1].Content != "work rules" {
		t.Errorf("ParseInline after replace = %+v", secs)
	}
}

func TestReplaceSection_unknownSectionIsLeftAlone(t *testing.T) {
	body := InlineBody([]SourceContent{{Name: "pers", Content: "pers rules"}})
	got, ok := ReplaceSection(body, "missing", "x")
	if ok {
		t.Error("ReplaceSection claimed to replace a section that is not there")
	}
	if got != body {
		t.Errorf("body changed: %q", got)
	}
}
