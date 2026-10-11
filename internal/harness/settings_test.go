package harness

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jophira/weft/internal/manifest"
)

// settingsEnv points the harness at a temp home and returns the live settings
// path, a config dir and an apply context writing to out.
func settingsEnv(t *testing.T) (live string, ctx ApplyCtx, out *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	withHarnessHome(t, home)
	out = &bytes.Buffer{}
	ctx = ApplyCtx{ProfileName: "p", CfgDir: filepath.Join(t.TempDir(), "cfg"), Out: out}
	return filepath.Join(home, ".claude", "settings.json"), ctx, out
}

func src(name, data string) SettingsSource {
	return SettingsSource{Name: name, Path: name + "/settings.json", Data: []byte(data)}
}

func writeLive(t *testing.T, path, data string) {
	t.Helper()
	writeAdoptFile(t, path, data)
}

func readDoc(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parsing %s: %v\n%s", path, err, b)
	}
	return doc
}

func backupCount(t *testing.T, cfgDir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(filepath.Join(cfgDir, "backups"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestProjectSettings(t *testing.T) {
	cc := &ClaudeCode{}

	t.Run("sets source keys and keeps unrelated live keys", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		writeLive(t, live, `{"model":"opus","big":12345678901234567890}`)
		if err := ProjectSettings(cc, []SettingsSource{src("a", `{"hooks":{"x":1}}`)}, ctx); err != nil {
			t.Fatal(err)
		}
		doc := readDoc(t, live)
		if string(doc["model"]) != `"opus"` || string(doc["big"]) != `12345678901234567890` {
			t.Errorf("unrelated keys changed: %s", doc)
		}
		if compactJSON(doc["hooks"]) != `{"x":1}` {
			t.Errorf("hooks = %s", doc["hooks"])
		}
	})

	t.Run("creates the file when missing", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		if err := ProjectSettings(cc, []SettingsSource{src("a", `{"a":1}`)}, ctx); err != nil {
			t.Fatal(err)
		}
		if string(readDoc(t, live)["a"]) != "1" {
			t.Error("key a not written")
		}
	})

	t.Run("removes an owned key dropped from the source and never an unowned one", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		writeLive(t, live, `{"keep":1}`)
		if err := ProjectSettings(cc, []SettingsSource{src("a", `{"x":1,"y":2}`)}, ctx); err != nil {
			t.Fatal(err)
		}
		if err := ProjectSettings(cc, []SettingsSource{src("a", `{"y":2}`)}, ctx); err != nil {
			t.Fatal(err)
		}
		doc := readDoc(t, live)
		if _, ok := doc["x"]; ok {
			t.Error("x should be removed")
		}
		if _, ok := doc["y"]; !ok {
			t.Error("y should stay")
		}
		if _, ok := doc["keep"]; !ok {
			t.Error("unowned key keep was removed")
		}
	})

	t.Run("dropping every owned key releases the file", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		if err := ProjectSettings(cc, []SettingsSource{src("a", `{"x":1}`)}, ctx); err != nil {
			t.Fatal(err)
		}
		if err := ProjectSettings(cc, nil, ctx); err != nil {
			t.Fatal(err)
		}
		m, err := manifest.Load(ctx.CfgDir, cc.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, owned := m.Files[settingsManifestKey(live)]; owned || len(m.Settings) != 0 {
			t.Errorf("ownership left behind: files=%v settings=%v", m.Files, m.Settings)
		}
	})

	t.Run("later source wins per key", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		err := ProjectSettings(cc, []SettingsSource{src("low", `{"k":1,"only":1}`), src("high", `{"k":2}`)}, ctx)
		if err != nil {
			t.Fatal(err)
		}
		doc := readDoc(t, live)
		if string(doc["k"]) != "2" || string(doc["only"]) != "1" {
			t.Errorf("got %s", doc)
		}
		m, _ := manifest.Load(ctx.CfgDir, "claude-code")
		if m.Settings[live]["k"] != "high" || m.Settings[live]["only"] != "low" {
			t.Errorf("owners = %v", m.Settings[live])
		}
	})

	t.Run("untouched when nothing is owned", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		const body = `{ "b": 1,   "a": 2 }`
		writeLive(t, live, body)
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(live, old, old); err != nil {
			t.Fatal(err)
		}
		if err := ProjectSettings(cc, nil, ctx); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(live)
		fi, _ := os.Stat(live)
		if string(b) != body || !fi.ModTime().Equal(old) {
			t.Errorf("file touched: %q %v", b, fi.ModTime())
		}
		if out.Len() != 0 {
			t.Errorf("unexpected log: %s", out)
		}
	})

	t.Run("unparseable live file errors and stays unchanged", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		writeLive(t, live, `{broken`)
		if err := ProjectSettings(cc, []SettingsSource{src("a", `{"a":1}`)}, ctx); err == nil {
			t.Fatal("want error")
		}
		if b, _ := os.ReadFile(live); string(b) != `{broken` {
			t.Errorf("file changed: %s", b)
		}
	})

	t.Run("source that is not an object errors naming its path", func(t *testing.T) {
		_, ctx, _ := settingsEnv(t)
		err := ProjectSettings(cc, []SettingsSource{src("a", `[1]`)}, ctx)
		if err == nil || !strings.Contains(err.Error(), "a/settings.json") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("withheld class is a no-op", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		ctx.AllowedClasses = map[Class]bool{ClassCommands: true}
		if err := ProjectSettings(cc, []SettingsSource{src("a", `{"a":1}`)}, ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(live); !os.IsNotExist(err) {
			t.Errorf("file written: %v", err)
		}
	})

	t.Run("no write or backup when live already means the same", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		const body = "{\"z\": 1,\n \"a\": {\"p\":  [1, 2]}}"
		writeLive(t, live, body)
		// Indented source value and a new unowned live key: still the same meaning.
		srcData := "{\"a\": {\n \"p\": [1,\n 2]}, \"z\": 1}"
		if err := ProjectSettings(cc, []SettingsSource{src("a", srcData)}, ctx); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(live); string(b) != body {
			t.Errorf("file rewritten: %q", b)
		}
		if n := backupCount(t, ctx.CfgDir); n != 0 {
			t.Errorf("%d backups", n)
		}
		if !strings.Contains(out.String(), "unchanged") {
			t.Errorf("want unchanged log, got %q", out)
		}
		m, _ := manifest.Load(ctx.CfgDir, "claude-code")
		if m.Files[settingsManifestKey(live)] != manifest.HashBytes([]byte(body)) {
			t.Error("live hash not recorded on the unchanged path")
		}
	})

	t.Run("second apply of the same source writes nothing", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		s := []SettingsSource{src("a", `{"b":1,"a":2}`)}
		if err := ProjectSettings(cc, s, ctx); err != nil {
			t.Fatal(err)
		}
		first, _ := os.ReadFile(live)
		out.Reset()
		if err := ProjectSettings(cc, s, ctx); err != nil {
			t.Fatal(err)
		}
		second, _ := os.ReadFile(live)
		if !bytes.Equal(first, second) || !strings.Contains(out.String(), "unchanged") || backupCount(t, ctx.CfgDir) != 0 {
			t.Errorf("not idempotent: %q", out)
		}
	})
}
