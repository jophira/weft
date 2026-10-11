package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/advice"
	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

func TestResolveSettings(t *testing.T) {
	cfgDir := t.TempDir()
	live := filepath.Join(t.TempDir(), ".claude", "settings.json")
	m := &manifest.Manifest{
		Harness: "claude-code", Profile: "work", Files: map[string]string{},
		Settings: map[string]map[string]string{live: {"hooks": "work-tech", "env": "pers", "permissions": "work-tech"}},
	}
	if err := manifest.Save(cfgDir, m); err != nil {
		t.Fatal(err)
	}

	t.Run("names the harness, profile and each source with its keys", func(t *testing.T) {
		var out bytes.Buffer
		found, err := resolveSettings(&out, cfgDir, live)
		if err != nil || !found {
			t.Fatalf("found=%v err=%v", found, err)
		}
		for _, want := range []string{"harness: claude-code", "profile: work", "work-tech (keys: hooks, permissions)", "pers (keys: env)"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output missing %q:\n%s", want, out.String())
			}
		}
	})

	t.Run("reports not found for a path no manifest owns", func(t *testing.T) {
		var out bytes.Buffer
		found, err := resolveSettings(&out, cfgDir, filepath.Join(t.TempDir(), "settings.json"))
		if err != nil || found || out.Len() != 0 {
			t.Errorf("found=%v err=%v out=%q", found, err, out.String())
		}
	})
}

func TestApplyProjectsSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := t.TempDir()

	live := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, live, `{"model":"opus"}`)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "# rules")
	writeFile(t, filepath.Join(root, "harness", "claude-code", "settings.json"),
		`{"statusLine":{"type":"command","command":"{{weft.root}}/bin/status.sh"}}`)
	srcs := []source.Source{{Name: "testsrc", Root: root, Priority: 10, Structure: source.DefaultStructure()}}
	p := &profile.Profile{Name: "settingstest", Sources: []string{"testsrc"}, Overlay: profile.OverlayCascade, Targets: []string{"claude-code"}}

	// A fresh bus so the assertion sees only this apply's advice.
	bus := advice.New(advice.Options{})
	advice.SetDefault(bus)
	t.Cleanup(func() { advice.SetDefault(advice.New(advice.Options{})) })

	if err := mergeAndApply(p, rootsOf(srcs), srcs, cfgDir, true); err != nil {
		t.Fatalf("mergeAndApply: %v", err)
	}

	var adviceOut bytes.Buffer
	bus.Emit(&adviceOut)
	if strings.Contains(adviceOut.String(), "outside every managed directory") {
		t.Errorf("harness/ file raised a source-not-synced hint:\n%s", adviceOut.String())
	}

	data, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, data)
	}
	if string(raw["model"]) != `"opus"` {
		t.Errorf("unrelated key lost:\n%s", data)
	}
	var sl map[string]string
	if err := json.Unmarshal(raw["statusLine"], &sl); err != nil {
		t.Fatalf("statusLine: %v\n%s", err, data)
	}
	if want := filepath.ToSlash(root) + "/bin/status.sh"; filepath.ToSlash(sl["command"]) != want {
		t.Errorf("anchor not expanded: got %q want %q", sl["command"], want)
	}
}

// TestSettingsSourcesEscapesAnchorPaths: an anchor expands inside a JSON string,
// so a root holding a backslash (every Windows path) must stay valid JSON.
func TestSettingsSourcesEscapesAnchorPaths(t *testing.T) {
	root := t.TempDir() + string(filepath.Separator) + `a\b`
	path := filepath.Join(root, "harness", "claude-code", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"cmd":"{{weft.root}}/x.sh"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := settingsSources([]string{root}, []source.Source{{Name: "s", Root: root}}, "claude-code")
	if err != nil || len(got) != 1 {
		t.Fatalf("settingsSources = %v, %v", got, err)
	}
	var doc map[string]string
	if err := json.Unmarshal(got[0].Data, &doc); err != nil {
		t.Fatalf("expanded settings are not valid JSON: %v\n%s", err, got[0].Data)
	}
	if doc["cmd"] != root+"/x.sh" {
		t.Errorf("cmd = %q, want %q", doc["cmd"], root+"/x.sh")
	}
}
