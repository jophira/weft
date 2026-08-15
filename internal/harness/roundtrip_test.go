package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/instruction"
	"github.com/jophira/weft/internal/testenv"
)

// ADR 0004 D6: every (class, harness) transform must satisfy
// toCanonical(toNative(x)) == x, enforced in CI. The MCP dialects are covered in
// internal/mcpconfig; these are the instruction-class pairs, which is to say the
// Tier B managed block and Cursor's .mdc frontmatter on top of it.
//
// Non-idempotency here is not cosmetic. Weft writes the block, the target
// watcher sees the file change, write-back parses it and rewrites the source,
// the source watcher re-applies, and the block is written again. A transform
// that cannot reproduce its input makes that loop run forever.

// roundTripSources are the canonical inputs every Tier B projection must
// reproduce. Content is given already trimmed of surrounding blank lines,
// because that is the canonical form: InlineBody trims, so anything else would
// be asserting an equality the format never promised.
func roundTripSources() []struct {
	name    string
	sources []instruction.SourceContent
} {
	return []struct {
		name    string
		sources []instruction.SourceContent
	}{
		{
			name:    "single source",
			sources: []instruction.SourceContent{{Name: "personal", Content: "# Rules\nBe brief."}},
		},
		{
			name: "several sources in priority order",
			sources: []instruction.SourceContent{
				{Name: "personal", Content: "# Personal\nrule one"},
				{Name: "work", Content: "# Work\nrule two"},
				{Name: "project", Content: "# Project\nrule three"},
			},
		},
		{
			name: "markdown with blank lines and horizontal rules",
			sources: []instruction.SourceContent{
				{Name: "prose", Content: "# Heading\n\nA paragraph.\n\n---\n\nAnother one."},
			},
		},
		{
			name: "html comments that are not weft markers",
			sources: []instruction.SourceContent{
				{Name: "commented", Content: "<!-- a note -->\ncontent\n<!-- another -->"},
			},
		},
		{
			name: "source names with spaces and punctuation",
			sources: []instruction.SourceContent{
				{Name: "my rules (personal)", Content: "x"},
				{Name: "ai-rules.work", Content: "y"},
			},
		},
		{
			name: "unicode and emoji",
			sources: []instruction.SourceContent{
				{Name: "国際", Content: "# 規則\nsimplicité 🚀"},
			},
		},
		{
			name: "content that looks like frontmatter",
			sources: []instruction.SourceContent{
				{Name: "fm", Content: "---\nalwaysApply: true\n---\nbody"},
			},
		},
		{
			name: "indented code fence",
			sources: []instruction.SourceContent{
				{Name: "code", Content: "```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```"},
			},
		},
	}
}

// TestManagedBlock_RoundTripIsIdentity is the Tier B pair on its own: the
// managed-block envelope must give back exactly the sources it was handed.
func TestManagedBlock_RoundTripIsIdentity(t *testing.T) {
	for _, tc := range roundTripSources() {
		t.Run(tc.name, func(t *testing.T) {
			native := instruction.Upsert(nil, instruction.InlineBody(tc.sources))
			body, found := instruction.Extract(native)
			if !found {
				t.Fatalf("no managed block in:\n%s", native)
			}
			if got := instruction.ParseInline(body); !reflect.DeepEqual(got, tc.sources) {
				t.Errorf("round trip changed the sources\n got: %#v\nwant: %#v\nnative:\n%s", got, tc.sources, native)
			}
		})
	}
}

// The user's own prose lives outside the block. A projection that reformats it,
// or moves the block relative to it, silently rewrites a file weft does not own.
func TestManagedBlock_RoundTripPreservesSurroundingContent(t *testing.T) {
	existing := []byte("# My notes\n\nkeep me exactly\n\n<!-- not a weft marker -->\n")
	updated := instruction.Upsert(existing, instruction.InlineBody(
		[]instruction.SourceContent{{Name: "s", Content: "rule"}}))

	if !strings.HasPrefix(string(updated), string(existing)) {
		t.Errorf("content before the block was rewritten:\n%s", updated)
	}

	again := instruction.Upsert(updated, instruction.InlineBody(
		[]instruction.SourceContent{{Name: "s", Content: "rule"}}))
	if string(again) != string(updated) {
		t.Errorf("re-projecting the same sources changed the file:\n%s\nwant:\n%s", again, updated)
	}
}

// inlineHarnesses returns every harness that projects instructions as an inline
// (Tier B) managed block, so the property covers whatever adapters exist rather
// than a list that goes stale the next time one is added.
func inlineHarnesses(t *testing.T) []Harness {
	t.Helper()
	var out []Harness
	for _, h := range Instances() {
		ic, ok := h.(InstructionConsumer)
		if !ok {
			continue
		}
		spec, err := ic.InstructionSpec()
		if err != nil || spec.Strategy != StrategyInline {
			continue
		}
		out = append(out, h)
	}
	return out
}

// TestTierBHarnesses_RoundTripIsIdentity drives the real projection path for
// every Tier B harness, then inverts it the way instructionWriteBack does.
func TestTierBHarnesses_RoundTripIsIdentity(t *testing.T) {
	for _, tc := range roundTripSources() {
		for _, h := range inlineHarnessesInHome(t) {
			t.Run(h.Name()+"/"+tc.name, func(t *testing.T) {
				home := t.TempDir()
				testenv.SetHome(t, home)
				testenv.ClearPath(t)

				sources := make([]SourceInstruction, len(tc.sources))
				for i, s := range tc.sources {
					sources[i] = SourceInstruction{Name: s.Name, Content: s.Content}
				}
				ctx := ApplyCtx{ProfileName: "test", CfgDir: filepath.Join(home, "cfg")}
				// stagedRoot "" skips the advertised index, which is generated from
				// files rather than from these sources and so is not part of this pair.
				if err := ProjectInstruction(h, "", sources, ctx); err != nil {
					t.Fatalf("ProjectInstruction: %v", err)
				}

				spec, err := h.(InstructionConsumer).InstructionSpec()
				if err != nil {
					t.Fatalf("InstructionSpec: %v", err)
				}
				native, err := os.ReadFile(spec.Path)
				if err != nil {
					t.Fatalf("reading %s: %v", spec.Path, err)
				}
				body, found := instruction.Extract(native)
				if !found {
					t.Fatalf("no managed block in %s:\n%s", spec.Path, native)
				}
				if got := instruction.ParseInline(body); !reflect.DeepEqual(got, tc.sources) {
					t.Errorf("round trip changed the sources\n got: %#v\nwant: %#v\nnative:\n%s",
						got, tc.sources, native)
				}

				// Re-projecting unchanged sources must not touch the file, or the
				// watcher would see a change it caused itself.
				if err := ProjectInstruction(h, "", sources, ctx); err != nil {
					t.Fatalf("second ProjectInstruction: %v", err)
				}
				again, err := os.ReadFile(spec.Path)
				if err != nil {
					t.Fatalf("re-reading %s: %v", spec.Path, err)
				}
				if string(again) != string(native) {
					t.Errorf("second projection changed %s:\n%s\nwant:\n%s", spec.Path, again, native)
				}
			})
		}
	}
}

// inlineHarnessesInHome resolves the Tier B set against a throwaway home, so
// enumerating the adapters cannot touch the developer's real config.
func inlineHarnessesInHome(t *testing.T) []Harness {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	testenv.ClearPath(t)
	return inlineHarnesses(t)
}

// Cursor loads a rule file only when the .mdc frontmatter survives at the top of
// it. The frontmatter is written once, when the file is created, and every later
// projection has to leave it alone — a projection that dropped it would leave a
// file Cursor silently ignores, which is the failure class ADR 0004 exists for.
func TestCursorMDC_FrontmatterSurvivesRoundTrip(t *testing.T) {
	for _, tc := range roundTripSources() {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			testenv.SetHome(t, home)
			testenv.ClearPath(t)

			c := &Cursor{}
			sources := make([]SourceInstruction, len(tc.sources))
			for i, s := range tc.sources {
				sources[i] = SourceInstruction{Name: s.Name, Content: s.Content}
			}
			ctx := ApplyCtx{ProfileName: "test", CfgDir: filepath.Join(home, "cfg")}
			mdc := filepath.Join(home, ".cursor", "rules", "weft.mdc")
			if err := ProjectInstruction(c, "", sources, ctx); err != nil {
				t.Fatalf("ProjectInstruction: %v", err)
			}
			native, err := os.ReadFile(mdc)
			if err != nil {
				t.Fatalf("reading weft.mdc: %v", err)
			}
			if !strings.HasPrefix(string(native), cursorMDCHeader) {
				t.Fatalf("weft.mdc must start with the always-apply frontmatter:\n%s", native)
			}
			// A second projection must not reseed the preamble it already wrote.
			if err := ProjectInstruction(c, "", sources, ctx); err != nil {
				t.Fatalf("second ProjectInstruction: %v", err)
			}
			again, err := os.ReadFile(mdc)
			if err != nil {
				t.Fatalf("re-reading weft.mdc: %v", err)
			}
			if string(again) != string(native) {
				t.Errorf("second projection changed weft.mdc:\n%s\nwant:\n%s", again, native)
			}
			body, found := instruction.Extract(native)
			if !found {
				t.Fatalf("no managed block in weft.mdc:\n%s", native)
			}
			if got := instruction.ParseInline(body); !reflect.DeepEqual(got, tc.sources) {
				t.Errorf("round trip changed the sources\n got: %#v\nwant: %#v\nnative:\n%s",
					got, tc.sources, native)
			}
		})
	}
}

// The user may add their own frontmatter keys. They sit outside the managed
// block, so weft must preserve them verbatim rather than reseeding its own.
func TestCursorMDC_PreservesUserFrontmatterKeys(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	testenv.ClearPath(t)

	mdc := filepath.Join(home, ".cursor", "rules", "weft.mdc")
	write(t, mdc, "---\nalwaysApply: true\ndescription: my own note\n---\n")

	ctx := ApplyCtx{ProfileName: "test", CfgDir: filepath.Join(home, "cfg")}
	if err := ProjectInstruction(&Cursor{}, "", []SourceInstruction{{Name: "s", Content: "rule"}}, ctx); err != nil {
		t.Fatalf("ProjectInstruction: %v", err)
	}

	native, err := os.ReadFile(mdc)
	if err != nil {
		t.Fatalf("reading weft.mdc: %v", err)
	}
	if !strings.Contains(string(native), "description: my own note") {
		t.Errorf("user frontmatter key was dropped:\n%s", native)
	}
}
