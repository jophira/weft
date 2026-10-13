package harness

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jophira/weft/internal/anchor"
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

// fileSrc is a source whose file really exists, so a write-back has somewhere
// to land. data is what the file holds; the expanded Data is the same bytes
// unless anchors are given.
func fileSrc(t *testing.T, name, data string, a anchor.Anchors) SettingsSource {
	t.Helper()
	p := filepath.Join(t.TempDir(), name, "settings.json")
	writeAdoptFile(t, p, data)
	return SettingsSource{Name: name, Path: p, Data: anchor.Expand([]byte(data), a), Anchors: a}
}

func TestProjectSettingsWriteBack(t *testing.T) {
	cc := &ClaudeCode{}
	const srcDoc = "{\n    \"model\": \"opus\",\n    \"permissions\": {\n        \"allow\": [\"a\"]\n    },\n    \"theme\": \"dark\"\n}\n"

	t.Run("harness edit to owned key is written back", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		s := fileSrc(t, "a", srcDoc, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"model":"opus","permissions":{"allow":["a","b"]},"theme":"dark"}`)
		out.Reset()
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, live); !strings.Contains(got, `"b"`) || !strings.Contains(got, `"a"`) {
			t.Errorf("live lost the harness edit: %s", got)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(readFile(t, s.Path)), &doc); err != nil {
			t.Fatal(err)
		}
		if compactJSON(doc["permissions"]) != `{"allow":["a","b"]}` {
			t.Errorf("source permissions = %s", doc["permissions"])
		}
		if !strings.Contains(out.String(), "written back to a") || backupCount(t, ctx.CfgDir) != 0 {
			t.Errorf("out = %q", out)
		}
		// And it is stable: the next apply from the updated source writes nothing.
		s.Data = []byte(readFile(t, s.Path))
		out.Reset()
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "unchanged") || strings.Contains(out.String(), "written back") {
			t.Errorf("not stable: %q", out)
		}
	})

	t.Run("owned key changed on both sides is held", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		s := fileSrc(t, "a", `{"k":1}`, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"k":2}`)
		s2 := fileSrc(t, "a", `{"k":3}`, anchor.Anchors{})
		out.Reset()
		if err := ProjectSettings(cc, []SettingsSource{s2}, ctx); err != nil {
			t.Fatal(err)
		}
		if string(readDoc(t, live)["k"]) != "2" || readFile(t, s2.Path) != `{"k":3}` {
			t.Errorf("a side was overwritten: live=%s source=%s", readFile(t, live), readFile(t, s2.Path))
		}
		if !strings.Contains(out.String(), "k changed in both a and the harness") || backupCount(t, ctx.CfgDir) != 0 {
			t.Errorf("out = %q", out)
		}
		// Editing one side to match clears it.
		writeLive(t, live, `{"k":3}`)
		out.Reset()
		if err := ProjectSettings(cc, []SettingsSource{s2}, ctx); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "changed in both") {
			t.Errorf("still held after the sides matched: %q", out)
		}
	})

	t.Run("unowned edit then source edit makes no backup", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		s := fileSrc(t, "a", `{"owned":1}`, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"owned":1,"effort":"high"}`) // /config
		s2 := fileSrc(t, "a", `{"owned":2}`, anchor.Anchors{})
		out.Reset()
		if err := ProjectSettings(cc, []SettingsSource{s2}, ctx); err != nil {
			t.Fatal(err)
		}
		doc := readDoc(t, live)
		if string(doc["owned"]) != "2" || string(doc["effort"]) != `"high"` {
			t.Errorf("live = %s", readFile(t, live))
		}
		if backupCount(t, ctx.CfgDir) != 0 || strings.Contains(out.String(), "changed outside weft") {
			t.Errorf("backup made: %q", out)
		}
	})

	t.Run("write back splices only the value", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		s := fileSrc(t, "a", srcDoc, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"model":"sonnet","permissions":{"allow":["a"]},"theme":"dark"}`)
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if want := strings.Replace(srcDoc, `"opus"`, `"sonnet"`, 1); readFile(t, s.Path) != want {
			t.Errorf("source =\n%s\nwant\n%s", readFile(t, s.Path), want)
		}
	})

	t.Run("write back collapses anchors", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		a := anchor.Anchors{Root: "/srv/src"}
		s := fileSrc(t, "a", `{"cmd":"{{weft.root}}/x.sh"}`, a)
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"cmd":"/srv/src/y.sh"}`)
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, s.Path); got != `{"cmd":"{{weft.root}}/y.sh"}` {
			t.Errorf("source = %s", got)
		}
	})

	t.Run("deletion is written back and keeps key order", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		s := fileSrc(t, "a", `{"z":1,"m":2,"a":3}`, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"z":1,"a":3}`)
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, s.Path); got != "{\n  \"z\": 1,\n  \"a\": 3\n}\n" {
			t.Errorf("source = %q", got)
		}
		if _, ok := readDoc(t, live)["m"]; ok {
			t.Error("deleted key came back")
		}
		m, _ := manifest.Load(ctx.CfgDir, "claude-code")
		if _, ok := m.Settings[live]["m"]; ok {
			t.Error("deleted key still owned")
		}
	})

	t.Run("deletion held when earlier source defines key", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		low := fileSrc(t, "low", `{"k":1}`, anchor.Anchors{})
		high := fileSrc(t, "high", `{"k":2,"j":3}`, anchor.Anchors{})
		both := []SettingsSource{low, high}
		if err := ProjectSettings(cc, both, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"j":3}`)
		out.Reset()
		if err := ProjectSettings(cc, both, ctx); err != nil {
			t.Fatal(err)
		}
		if readFile(t, high.Path) != `{"k":2,"j":3}` || !strings.Contains(out.String(), "another source also defines it") {
			t.Errorf("source touched or not held: %q", out)
		}
	})

	t.Run("held deletion clears when the winning source drops the key", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		low := fileSrc(t, "low", `{"k":1}`, anchor.Anchors{})
		high := fileSrc(t, "high", `{"k":2,"j":3}`, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{low, high}, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"j":3}`)
		high.Data = []byte(`{"j":3}`)
		out.Reset()
		if err := ProjectSettings(cc, []SettingsSource{low, high}, ctx); err != nil {
			t.Fatal(err)
		}
		if string(readDoc(t, live)["k"]) != "1" || strings.Contains(out.String(), "changed in both") {
			t.Errorf("earlier source's value not restored, or still held: %s %q", readFile(t, live), out)
		}
	})

	t.Run("missing live file is restored and never empties the source", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		s := fileSrc(t, "a", srcDoc, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(live); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if readFile(t, s.Path) != srcDoc {
			t.Errorf("source changed: %s", readFile(t, s.Path))
		}
		if string(readDoc(t, live)["model"]) != `"opus"` || strings.Contains(out.String(), "written back") {
			t.Errorf("live not restored: %s %q", readFile(t, live), out)
		}
	})

	t.Run("live file holding no recorded key is restored, not written back", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		s := fileSrc(t, "a", srcDoc, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"effort":"high"}`)
		out.Reset()
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if readFile(t, s.Path) != srcDoc {
			t.Errorf("source changed: %s", readFile(t, s.Path))
		}
		doc := readDoc(t, live)
		if string(doc["model"]) != `"opus"` || string(doc["effort"]) != `"high"` {
			t.Errorf("live not restored around the unowned key: %s", readFile(t, live))
		}
	})

	t.Run("write back keeps the source dir mode and writes through a symlink", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		orig := fileSrc(t, "a", `{"k":1,"j":2}`, anchor.Anchors{})
		dir := filepath.Dir(orig.Path)
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "settings.json")
		if err := os.Symlink(orig.Path, link); err != nil {
			t.Fatal(err)
		}
		s := orig
		s.Path = link
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"k":5,"j":2}`)
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("symlink replaced: %v %v", fi, err)
		}
		if got := readFile(t, orig.Path); !strings.Contains(got, `"k":5`) {
			t.Errorf("orig file not updated: %s", got)
		}
		// Windows reports no Unix permission bits, so only the symlink half applies there.
		if fi, _ := os.Stat(dir); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o755 {
			t.Errorf("source dir mode = %v", fi.Mode().Perm())
		}
	})

	t.Run("deleting every recorded key counts as a reset and is restored", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		s := fileSrc(t, "a", `{"k":1}`, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{}`)
		out.Reset()
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if readFile(t, s.Path) != `{"k":1}` || string(readDoc(t, live)["k"]) != "1" {
			t.Errorf("not restored: source %s live %s", readFile(t, s.Path), readFile(t, live))
		}
	})

	t.Run("dropped key edited in harness is released", func(t *testing.T) {
		live, ctx, out := settingsEnv(t)
		if err := ProjectSettings(cc, []SettingsSource{src("a", `{"x":1,"y":2}`)}, ctx); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"x":9,"y":2}`)
		out.Reset()
		if err := ProjectSettings(cc, []SettingsSource{src("a", `{"y":2}`)}, ctx); err != nil {
			t.Fatal(err)
		}
		if string(readDoc(t, live)["x"]) != "9" || strings.Contains(out.String(), "changed in both") {
			t.Errorf("edit lost or warned: %s %q", readFile(t, live), out)
		}
		m, _ := manifest.Load(ctx.CfgDir, "claude-code")
		if _, ok := m.Settings[live]["x"]; ok {
			t.Error("released key still owned")
		}
	})

	t.Run("failed source write back keeps live value", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores file modes")
		}
		live, ctx, out := settingsEnv(t)
		s := fileSrc(t, "a", `{"k":1}`, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(s.Path, 0o400); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"k":2}`)
		for i := 0; i < 2; i++ {
			out.Reset()
			if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
				t.Fatal(err)
			}
			if string(readDoc(t, live)["k"]) != "2" {
				t.Fatalf("apply %d reverted the edit: %s", i+1, readFile(t, live))
			}
			if !strings.Contains(out.String(), "could not be written back") {
				t.Errorf("apply %d out = %q", i+1, out)
			}
		}
	})

	t.Run("concurrent write between read and write survives", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		s := fileSrc(t, "a", `{"a":1}`, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		s2 := fileSrc(t, "a", `{"a":2}`, anchor.Anchors{})
		beforeSettingsWrite = func() {
			beforeSettingsWrite = nil
			writeLive(t, live, `{"a":1,"late":true}`)
		}
		t.Cleanup(func() { beforeSettingsWrite = nil })
		if err := ProjectSettings(cc, []SettingsSource{s2}, ctx); err != nil {
			t.Fatal(err)
		}
		doc := readDoc(t, live)
		if string(doc["late"]) != "true" || string(doc["a"]) != "2" {
			t.Errorf("live = %s", readFile(t, live))
		}
	})

	t.Run("manifest without a record behaves as before", func(t *testing.T) {
		live, ctx, _ := settingsEnv(t)
		s := fileSrc(t, "a", `{"k":1}`, anchor.Anchors{})
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		m, _ := manifest.Load(ctx.CfgDir, "claude-code")
		m.SettingsValues = nil // as written by an older weft
		if err := manifest.Save(ctx.CfgDir, m); err != nil {
			t.Fatal(err)
		}
		writeLive(t, live, `{"k":2}`)
		if err := ProjectSettings(cc, []SettingsSource{s}, ctx); err != nil {
			t.Fatal(err)
		}
		if string(readDoc(t, live)["k"]) != "1" || readFile(t, s.Path) != `{"k":1}` || backupCount(t, ctx.CfgDir) != 1 {
			t.Errorf("legacy path changed: live=%s backups=%d", readFile(t, live), backupCount(t, ctx.CfgDir))
		}
	})
}
