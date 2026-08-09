package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/testenv"
	"gopkg.in/yaml.v3"
)

const testConventions = "/home/u/.aider/CONVENTIONS.md"

// readEntries decodes the `read` key back out, normalising scalar and list forms
// so assertions do not care which shape the merge produced.
func readEntries(t *testing.T, doc []byte) []string {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("result is not valid YAML: %v\n%s", err, doc)
	}
	switch v := parsed["read"].(type) {
	case nil:
		return nil
	case string:
		return []string{v}
	case []any:
		out := make([]string, len(v))
		for i, e := range v {
			s, ok := e.(string)
			if !ok {
				t.Fatalf("read[%d] is %T, want string", i, e)
			}
			out[i] = s
		}
		return out
	default:
		t.Fatalf("read is %T, want string or list", v)
		return nil
	}
}

func TestMergeAiderRead_CreatesFile(t *testing.T) {
	got, err := mergeAiderRead(nil, testConventions)
	if err != nil {
		t.Fatal(err)
	}
	if entries := readEntries(t, got); len(entries) != 1 || entries[0] != testConventions {
		t.Errorf("read = %v, want [%s]", entries, testConventions)
	}
}

func TestMergeAiderRead_PreservesOtherKeys(t *testing.T) {
	existing := []byte("model: gpt-4o\nauto-commits: false\n")

	got, err := mergeAiderRead(existing, testConventions)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal(got, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["model"] != "gpt-4o" {
		t.Errorf("model = %v, want gpt-4o", parsed["model"])
	}
	if parsed["auto-commits"] != false {
		t.Errorf("auto-commits = %v, want false", parsed["auto-commits"])
	}
}

// TestMergeAiderRead_PreservesComments is the reason this works on the node tree
// rather than a map round-trip. The config is hand-written and the comments in
// it are the user's.
func TestMergeAiderRead_PreservesComments(t *testing.T) {
	existing := []byte("# my aider setup\nmodel: gpt-4o # the cheap one\n")

	got, err := mergeAiderRead(existing, testConventions)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# my aider setup", "# the cheap one"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("comment %q was dropped\ngot:\n%s", want, got)
		}
	}
}

// TestMergeAiderRead_PromotesScalar covers aider accepting a single --read as a
// bare string. The user's entry has to survive alongside weft's.
func TestMergeAiderRead_PromotesScalar(t *testing.T) {
	existing := []byte("read: /home/u/notes.md\n")

	got, err := mergeAiderRead(existing, testConventions)
	if err != nil {
		t.Fatal(err)
	}
	entries := readEntries(t, got)
	if len(entries) != 2 || entries[0] != "/home/u/notes.md" || entries[1] != testConventions {
		t.Errorf("read = %v, want both the existing entry and %s", entries, testConventions)
	}
}

func TestMergeAiderRead_AppendsToList(t *testing.T) {
	existing := []byte("read:\n  - /home/u/a.md\n  - /home/u/b.md\n")

	got, err := mergeAiderRead(existing, testConventions)
	if err != nil {
		t.Fatal(err)
	}
	entries := readEntries(t, got)
	if len(entries) != 3 || entries[2] != testConventions {
		t.Errorf("read = %v, want the two existing entries plus %s", entries, testConventions)
	}
}

// TestMergeAiderRead_Idempotent pins the property Wire depends on: every apply
// calls it, and none of them may grow the list.
func TestMergeAiderRead_Idempotent(t *testing.T) {
	once, err := mergeAiderRead(nil, testConventions)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := mergeAiderRead(once, testConventions)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Errorf("second merge changed the document\nfirst:\n%s\nsecond:\n%s", once, twice)
	}
	if entries := readEntries(t, twice); len(entries) != 1 {
		t.Errorf("read = %v, want a single entry after two merges", entries)
	}
}

func TestMergeAiderRead_IdempotentAgainstScalar(t *testing.T) {
	existing := []byte("read: " + testConventions + "\n")

	got, err := mergeAiderRead(existing, testConventions)
	if err != nil {
		t.Fatal(err)
	}
	if entries := readEntries(t, got); len(entries) != 1 || entries[0] != testConventions {
		t.Errorf("read = %v, want the scalar left alone", entries)
	}
}

// TestMergeAiderRead_RefusesUnparseable: rewriting a file weft cannot read would
// destroy config it has no claim over.
func TestMergeAiderRead_RefusesUnparseable(t *testing.T) {
	if _, err := mergeAiderRead([]byte("model: [unclosed\n"), testConventions); err == nil {
		t.Error("expected an error for unparseable YAML, got nil")
	}
}

func TestMergeAiderRead_RefusesNonMapping(t *testing.T) {
	if _, err := mergeAiderRead([]byte("- a\n- b\n"), testConventions); err == nil {
		t.Error("expected an error for a non-mapping document, got nil")
	}
}

// TestMergeAiderRead_CommentOnlyFile: a file holding only comments parses to an
// empty document, which must gain a mapping rather than error.
func TestMergeAiderRead_CommentOnlyFile(t *testing.T) {
	got, err := mergeAiderRead([]byte("# nothing set yet\n"), testConventions)
	if err != nil {
		t.Fatal(err)
	}
	if entries := readEntries(t, got); len(entries) != 1 {
		t.Errorf("read = %v, want one entry", entries)
	}
}

// ── Wire ──────────────────────────────────────────────────────────────────────

func TestAiderWire_WritesConfPointingAtConventions(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	if err := (&Aider{}).Wire(testCtx(t)); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".aider.conf.yml"))
	if err != nil {
		t.Fatalf("conf file not written: %v", err)
	}
	want := filepath.Join(home, ".aider", "CONVENTIONS.md")
	if entries := readEntries(t, data); len(entries) != 1 || entries[0] != want {
		t.Errorf("read = %v, want [%s]", entries, want)
	}
}

func TestAiderWire_PreservesExistingConfig(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	conf := filepath.Join(home, ".aider.conf.yml")
	if err := os.WriteFile(conf, []byte("model: gpt-4o\nread: /home/u/notes.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Aider{}).Wire(testCtx(t)); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "gpt-4o") {
		t.Errorf("existing config was dropped\ngot:\n%s", data)
	}
	if entries := readEntries(t, data); len(entries) != 2 {
		t.Errorf("read = %v, want the existing entry kept alongside weft's", entries)
	}
}

// TestAiderWire_RepeatedApplies guards the real-world path: apply runs Wire every
// time, and the conf must not grow.
func TestAiderWire_RepeatedApplies(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	ctx := testCtx(t)

	for i := range 3 {
		if err := (&Aider{}).Wire(ctx); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(home, ".aider.conf.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if entries := readEntries(t, data); len(entries) != 1 {
		t.Errorf("read = %v, want a single entry after three applies", entries)
	}
}

// TestProjectWiring_SkipsWhenInstructionsWithheld: the pointer exists only to
// deliver instructions, so a profile that withholds them must not get one.
func TestProjectWiring_SkipsWhenInstructionsWithheld(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	ctx := testCtx(t)
	ctx.AllowedClasses = map[Class]bool{ClassInstructions: false}

	if err := ProjectWiring(&Aider{}, ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".aider.conf.yml")); !os.IsNotExist(err) {
		t.Error("conf file was written despite instructions being withheld")
	}
}

// TestProjectWiring_NoOpForOtherHarnesses: every other adapter loads a
// well-known path and must be left alone.
func TestProjectWiring_NoOpForOtherHarnesses(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	for _, h := range []Harness{&ClaudeCode{}, &Codex{}, &Cursor{}, &Windsurf{}} {
		if err := ProjectWiring(h, testCtx(t)); err != nil {
			t.Errorf("%s: %v", h.Name(), err)
		}
	}
}
