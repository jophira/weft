package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jophira/weft/internal/rules"
)

// ruleTree writes a one-rule tree with a CEL detect of "true" and returns its
// root, so resolution always selects the single labelled rule.
func ruleTree(t *testing.T, label, body string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, label+".md"),
		"---\nlabel: "+label+"\ndetect: \"true\"\n---\n"+body)
	return root
}

func TestResolveAcrossRoots_LayersInOrder(t *testing.T) {
	repo := t.TempDir()
	personal := ruleTree(t, "personal", "PERSONAL")
	work := ruleTree(t, "work", "WORK")

	roots := []namedRoot{
		{Name: "personal", Root: personal},
		{Name: "work", Root: work},
	}
	ress, err := resolveAcrossRoots(repo, roots, rules.CacheOptions{Disabled: true})
	if err != nil {
		t.Fatalf("resolveAcrossRoots: %v", err)
	}
	if len(ress) != 2 {
		t.Fatalf("expected 2 resolutions, got %d", len(ress))
	}
	if got, want := layerBundles(ress), "PERSONAL\n\nWORK"; got != want {
		t.Errorf("layered bundle = %q, want %q", got, want)
	}
}

// TestResolveAcrossRoots_SkipsBadNamedSource proves a profile source that cannot
// be resolved is skipped (with a warning) rather than aborting the resolve,
// while an unnamed explicit root would be fatal.
func TestResolveAcrossRoots_SkipsBadNamedSource(t *testing.T) {
	repo := t.TempDir()
	good := ruleTree(t, "good", "GOOD")
	roots := []namedRoot{
		{Name: "good", Root: good},
		{Name: "missing", Root: filepath.Join(t.TempDir(), "does-not-exist")},
	}
	ress, err := resolveAcrossRoots(repo, roots, rules.CacheOptions{Disabled: true})
	if err != nil {
		t.Fatalf("named-source failure should not abort: %v", err)
	}
	if len(ress) != 1 || ress[0].Source != "good" {
		t.Errorf("expected only the good source resolved, got %+v", ress)
	}
}

func TestResolveAcrossRoots_ExplicitRootErrorIsFatal(t *testing.T) {
	repo := t.TempDir()
	// An unnamed root stands for an explicit --rules-root, whose failure is fatal.
	roots := []namedRoot{{Root: filepath.Join(t.TempDir(), "nope")}}
	if _, err := resolveAcrossRoots(repo, roots, rules.CacheOptions{Disabled: true}); err == nil {
		t.Error("expected explicit-root resolution failure to be fatal")
	}
}

func TestBuildResolveRecord_FlattensSourcesInOrder(t *testing.T) {
	ress := []sourceResolution{
		{Source: "personal", Res: rules.Resolution{Loaded: []rules.LoadedRule{{Label: "p-common", Body: "P"}}}},
		{Source: "work", Res: rules.Resolution{Loaded: []rules.LoadedRule{{Label: "w-common", Body: "W"}}}},
	}
	rec := buildResolveRecord("/repo", "hybrid", ress, time.Unix(0, 0).UTC())
	if rec.Profile != "hybrid" || rec.Repo != "/repo" {
		t.Errorf("unexpected record header: %+v", rec)
	}
	if len(rec.Loaded) != 2 ||
		rec.Loaded[0].Source != "personal" || rec.Loaded[0].Label != "p-common" ||
		rec.Loaded[1].Source != "work" || rec.Loaded[1].Label != "w-common" {
		t.Errorf("loaded entries not flattened in order: %+v", rec.Loaded)
	}
	if rec.ResolutionHash == "" {
		t.Error("expected a resolution hash")
	}
}

func TestLayerBundles_DropsEmpty(t *testing.T) {
	ress := []sourceResolution{
		{Res: rules.Resolution{}},
		{Res: rules.Resolution{Loaded: []rules.LoadedRule{{Body: "ONLY"}}}},
	}
	if got := layerBundles(ress); got != "ONLY" {
		t.Errorf("layerBundles = %q, want %q", got, "ONLY")
	}
}

func TestPrintResolveManifest_FlatVsWrapped(t *testing.T) {
	repo := t.TempDir()
	single := []sourceResolution{{Root: "/r", Res: rules.Resolution{}}}
	multi := []sourceResolution{
		{Source: "a", Root: "/a", Res: rules.Resolution{}},
		{Source: "b", Root: "/b", Res: rules.Resolution{}},
	}

	var flat bytes.Buffer
	if err := printResolveManifest(&flat, single, repo, ""); err != nil {
		t.Fatalf("flat manifest: %v", err)
	}
	var flatObj map[string]json.RawMessage
	if err := json.Unmarshal(flat.Bytes(), &flatObj); err != nil {
		t.Fatalf("flat json: %v", err)
	}
	if _, ok := flatObj["resolution_hash"]; !ok {
		t.Error("flat manifest should have resolution_hash")
	}
	if _, ok := flatObj["sources"]; ok {
		t.Error("flat manifest should not be wrapped")
	}

	var wrapped bytes.Buffer
	if err := printResolveManifest(&wrapped, multi, repo, "hybrid"); err != nil {
		t.Fatalf("wrapped manifest: %v", err)
	}
	if !strings.Contains(wrapped.String(), "\"profile\": \"hybrid\"") {
		t.Errorf("wrapped manifest should name the profile:\n%s", wrapped.String())
	}
	var wrapObj struct {
		Sources []json.RawMessage `json:"sources"`
	}
	if err := json.Unmarshal(wrapped.Bytes(), &wrapObj); err != nil {
		t.Fatalf("wrapped json: %v", err)
	}
	if len(wrapObj.Sources) != 2 {
		t.Errorf("expected 2 source manifests, got %d", len(wrapObj.Sources))
	}
}
