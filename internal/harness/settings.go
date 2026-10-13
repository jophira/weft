package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jophira/weft/internal/advice"
	"github.com/jophira/weft/internal/anchor"
	"github.com/jophira/weft/internal/locate"
	"github.com/jophira/weft/internal/manifest"
)

// settingsFileName is the file a source supplies and a harness reads.
const settingsFileName = "settings.json"

// SettingsOwner is an optional Harness extension for tools whose settings.json
// weft projects key by key (currently Claude Code).
type SettingsOwner interface {
	// SettingsPath is the absolute path of the live settings file.
	SettingsPath() (string, error)
}

// SettingsSource is one source's settings document, already anchor-expanded.
type SettingsSource struct {
	Name    string // source name, recorded as the owner of each key
	Path    string // where the bytes came from, for error messages and write-back
	Data    []byte
	Anchors anchor.Anchors // what Data was expanded with, so a written-back value can be collapsed
}

// SettingsSourcePath is where a source root keeps a harness's settings file.
func SettingsSourcePath(root, harnessName string) string {
	return filepath.Join(root, "harness", harnessName, settingsFileName)
}

// settingsManifestKey is the sidecar Files key for a live settings file.
func settingsManifestKey(path string) string {
	return "settings:" + filepath.ToSlash(path)
}

// beforeSettingsWrite is a test hook called between ProjectSettings reading the
// live file and re-reading it just before the write.
var beforeSettingsWrite func()

// settingsPlan is what one pass over the live file decided.
type settingsPlan struct {
	next      map[string]json.RawMessage // the document to write live
	writeBack []string                   // keys whose live value goes to the owning source
	held      []string                   // keys changed on both sides
	shadowed  []string                   // keys the harness deleted that an earlier source also defines
	backup    bool                       // an owned key without a record would be overwritten
}

// ProjectSettings merges the sources' top-level keys into the harness's live
// settings file. Later sources win per key. Keys weft wrote on an earlier apply
// and no source names any more are removed; every other live key is untouched.
//
// A key the harness edited since weft last wrote it (Claude Code's /permissions
// adds an allow rule this way) is written back to the source that owns it
// rather than reverted. When the source changed the same key too, both values
// are kept and the key is reported as held, so neither side's edit is lost.
//
// It is a no-op when the class is withheld, the harness has no settings file to
// manage, or no source supplies settings and weft never owned a key there. In
// that last case the live file is not even read, so users without source
// settings never get their file reformatted.
func ProjectSettings(h Harness, srcs []SettingsSource, ctx ApplyCtx) error {
	owner, ok := h.(SettingsOwner)
	if !ok || !ctx.classAllowed(ClassSettings) {
		return nil
	}
	path, err := owner.SettingsPath()
	if err != nil {
		return fmt.Errorf("resolving %s settings path: %w", h.Name(), err)
	}

	want := map[string]json.RawMessage{}
	owners := map[string]string{}
	definers := map[string]int{} // how many sources define each key
	srcByName := map[string]SettingsSource{}
	for _, s := range srcs {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(s.Data, &doc); err != nil || doc == nil {
			return fmt.Errorf("%s is not a JSON object: %v", s.Path, err)
		}
		srcByName[s.Name] = s
		for k, v := range doc {
			want[k] = v
			owners[k] = s.Name
			definers[k]++
		}
	}

	m, err := manifest.Load(ctx.CfgDir, h.Name())
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}
	prev := m.Settings[path]
	if len(want) == 0 && len(prev) == 0 {
		return nil
	}
	rec := m.SettingsValues[path]

	live, cur, err := readLiveSettings(path)
	if err != nil {
		return err
	}
	plan := classifySettings(cur, want, owners, prev, rec, definers)
	if beforeSettingsWrite != nil {
		beforeSettingsWrite()
	}
	// Claude Code writes this file while a session runs. If it moved since the
	// read, plan again from what is there now, once; a second move is not chased,
	// so the overwrite is backed up instead.
	again, curAgain, err := readLiveSettings(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(again, live) {
		live, cur = again, curAgain
		plan = classifySettings(cur, want, owners, prev, rec, definers)
		if last, _, _ := readLiveSettings(path); !bytes.Equal(last, live) {
			plan.backup = len(live) > 0
		}
	}

	out := applyOut(ctx)
	display := locate.Tilde(path)
	key := settingsManifestKey(path)
	if plan.backup && len(live) > 0 {
		backupDir, bErr := backupConflicts([]conflictFile{{rel: filepath.Base(path), abs: path}}, h.Name(), ctx.CfgDir)
		if bErr != nil {
			return bErr
		}
		fmt.Fprintf(out, "  ! %s changed outside weft — backed up to %s\n", display, locate.Tilde(backupDir))
	}

	final := live
	if sameSettings(cur, plan.next) {
		fmt.Fprintf(out, logUnchanged, statusUnchanged, display)
	} else {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(plan.next); err != nil {
			return fmt.Errorf("encoding %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("creating parent dir for %s: %w", path, err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil { //nolint:gosec // path resolved from harness config
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Fprintf(out, logWrote, statusWrote, display)
		final = buf.Bytes()
	}

	// Source write-backs run after the live file is settled: a failure leaves the
	// record alone, so the next apply retries instead of reverting the edit.
	failed := map[string]bool{}
	for _, k := range plan.writeBack {
		v, present := plan.next[k]
		src, found := srcByName[owners[k]]
		var wErr error
		if found {
			wErr = writeBackSettingsKey(src, k, v, present)
		} else {
			wErr = fmt.Errorf("no source named %q", owners[k])
		}
		if wErr != nil {
			failed[k] = true
			fmt.Fprintf(out, "  ! %s: %s could not be written back to %s: %v\n", display, k, owners[k], wErr)
			advice.Add(advice.Advice{
				Code:     advice.CodeWriteBackFailed,
				Severity: advice.Warn,
				Message:  fmt.Sprintf("settings key %s changed in the harness but could not be written back to %s", k, owners[k]),
				Fix:      "make the source file writable, then run weft apply",
			})
			continue
		}
		fmt.Fprintf(out, "  ← %s: %s written back to %s\n", display, k, owners[k])
		if !present {
			delete(owners, k) // the source no longer defines it
		}
	}
	for _, k := range plan.held {
		fmt.Fprintf(out, "  ! %s: %s changed in both %s and the harness — kept both, edit one to match\n", display, k, owners[k])
	}
	for _, k := range plan.shadowed {
		fmt.Fprintf(out, "  ! %s: %s was removed in the harness but another source also defines it — remove it from every source, or re-add it\n", display, k)
	}
	stuck := append(append([]string{}, plan.held...), plan.shadowed...)
	if len(stuck) > 0 {
		advice.Add(advice.Advice{
			Code:     advice.CodeSettingsKeyHeld,
			Severity: advice.Warn,
			Message:  fmt.Sprintf("settings key(s) %s held: the harness and the sources disagree", strings.Join(stuck, ", ")),
			Fix:      "see the apply output for each key, then edit the source or the harness so they agree",
		})
	}

	held := map[string]bool{}
	for _, k := range stuck {
		held[k] = true
	}
	values := make(map[string]string, len(owners))
	for k := range owners {
		if held[k] || failed[k] {
			if r, ok := rec[k]; ok {
				values[k] = r
			}
			continue
		}
		if v, ok := plan.next[k]; ok {
			values[k] = settingValueHash(v)
		}
	}

	m.Harness = h.Name()
	if ctx.ProfileName != "" {
		m.Profile = ctx.ProfileName
	}
	if len(owners) == 0 {
		// Owning nothing means the file is the user's again: forget its hash too,
		// or adopt would refuse it as "already managed" for good.
		delete(m.Settings, path)
		delete(m.SettingsValues, path)
		delete(m.Files, key)
	} else {
		m.Settings[path] = owners
		m.SettingsValues[path] = values
		if len(final) > 0 {
			m.Files[key] = manifest.HashBytes(final)
		}
	}
	return manifest.Save(ctx.CfgDir, m)
}

// readLiveSettings reads the live settings file. A missing or blank file reads
// as a nil document, which classifySettings tells apart from an empty object; anything that is not a JSON object is an error and is
// never overwritten.
func readLiveSettings(path string) ([]byte, map[string]json.RawMessage, error) {
	live, err := os.ReadFile(path) //nolint:gosec // path resolved from the harness, not user input
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var cur map[string]json.RawMessage
	if len(bytes.TrimSpace(live)) > 0 {
		if err := json.Unmarshal(live, &cur); err != nil || cur == nil {
			return nil, nil, fmt.Errorf("%s is not a JSON object, refusing to overwrite it: %v", path, err)
		}
	}
	return live, cur, nil
}

// classifySettings decides, key by key, what the live document becomes. It writes
// nothing. prev lists the keys weft owned, rec the hash of each value as weft
// left it; a key without a record behaves as it did before records existed
// (the source wins).
func classifySettings(cur, want map[string]json.RawMessage, owners, prev, rec map[string]string, definers map[string]int) settingsPlan {
	p := settingsPlan{next: make(map[string]json.RawMessage, len(cur)+len(want))}
	for k, v := range cur {
		p.next[k] = v
	}
	for k := range prev {
		if _, stillWanted := want[k]; stillWanted {
			continue
		}
		// The source dropped the key. If the harness edited it since, the value is
		// the user's now: keep it rather than delete their edit.
		if liveV, inLive := cur[k]; inLive && rec[k] != "" && settingValueHash(liveV) != rec[k] {
			continue
		}
		delete(p.next, k)
	}
	// A file holding none of the keys weft recorded was reset or regenerated, not
	// edited key by key: restore it rather than empty the sources.
	reset := cur == nil
	if !reset && len(rec) > 0 {
		reset = true
		for k := range rec {
			if _, ok := cur[k]; ok {
				reset = false
				break
			}
		}
	}
	for k, srcV := range want {
		r := rec[k]
		liveV, inLive := cur[k]
		if r == "" {
			if inLive && compactJSON(liveV) != compactJSON(srcV) {
				p.backup = true
			}
			p.next[k] = srcV
			continue
		}
		if inLive && settingValueHash(liveV) == r {
			p.next[k] = srcV // the harness did not touch it; the source is the truth
			continue
		}
		if reset {
			p.next[k] = srcV
			continue
		}
		if !inLive && owners[k] != prev[k] {
			// The key the harness deleted now comes from a different source than
			// the value weft wrote, so the owner removed it: the next source's
			// value is the truth, or the deletion would stay held for good.
			p.next[k] = srcV
			continue
		}
		// The harness moved the key (edited or deleted it).
		switch {
		case settingValueHash(srcV) == r:
			if !inLive && definers[k] > 1 {
				// Deleting it from the winning source would bring an earlier
				// source's value back on the next apply.
				p.shadowed = append(p.shadowed, k)
			} else {
				p.writeBack = append(p.writeBack, k)
			}
		case inLive && compactJSON(srcV) == compactJSON(liveV):
			p.next[k] = srcV // both sides moved to the same value
		default:
			p.held = append(p.held, k)
		}
	}
	sort.Strings(p.writeBack)
	sort.Strings(p.held)
	sort.Strings(p.shadowed)
	return p
}

// settingValueHash identifies a value by its compact JSON, so re-indenting is
// not an edit.
func settingValueHash(v json.RawMessage) string {
	return manifest.HashBytes([]byte(compactJSON(v)))
}

// writeBackSettingsKey puts the harness's value for k into the source file
// s.Path. Replacing a key splices only that value's bytes, so a hand-written
// file keeps its layout. Adding or removing a key re-encodes the document with
// a 2-space indent, in the file's own key order.
func writeBackSettingsKey(s SettingsSource, k string, v json.RawMessage, present bool) error {
	data, err := os.ReadFile(s.Path) //nolint:gosec // path is under a registered source root
	if err != nil {
		return err
	}
	keys, spans, err := topLevelSpans(data)
	if err != nil {
		return fmt.Errorf("%s: %w", s.Path, err)
	}
	var out []byte
	if span, exists := spans[k]; exists && present {
		val := anchor.Collapse(v, s.Anchors)
		out = append(append(append([]byte{}, data[:span[0]]...), val...), data[span[1]:]...)
	} else {
		var buf bytes.Buffer
		buf.WriteString("{")
		first := true
		emit := func(name string, raw []byte) error {
			kb, mErr := json.Marshal(name)
			if mErr != nil {
				return mErr
			}
			if !first {
				buf.WriteString(",")
			}
			first = false
			buf.WriteString("\n  ")
			buf.Write(kb)
			buf.WriteString(": ")
			return json.Indent(&buf, raw, "  ", "  ")
		}
		for _, name := range keys {
			if name == k {
				continue
			}
			sp := spans[name]
			if err := emit(name, data[sp[0]:sp[1]]); err != nil {
				return err
			}
		}
		if present {
			if err := emit(k, anchor.Collapse(v, s.Anchors)); err != nil {
				return err
			}
		}
		buf.WriteString("\n}\n")
		out = buf.Bytes()
	}
	return replaceSourceFile(s.Path, out)
}

// replaceSourceFile atomically replaces a file in the user's source tree, so a
// crash mid-write cannot leave it half-written. It writes through a symlink to
// the real file (a dotfiles layout keeps working), keeps the file's mode, and
// never touches the directory's: privatefile is for weft's own state only.
func replaceSourceFile(path string, data []byte) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o200 == 0 {
		// The rename below would replace a file its owner made read-only.
		return fmt.Errorf("%s is read-only: %w", path, os.ErrPermission)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*.tmp")
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// topLevelSpans returns the keys of a JSON object in file order and the byte
// span [start, end) of each key's value. A repeated key keeps its last span,
// matching what json.Unmarshal into a map reads.
func topLevelSpans(data []byte) ([]string, map[string][2]int, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, nil, errors.New("not a JSON object")
	}
	var keys []string
	spans := map[string][2]int{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		name, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, nil, err
		}
		end := int(dec.InputOffset())
		if _, seen := spans[name]; !seen {
			keys = append(keys, name)
		}
		spans[name] = [2]int{end - len(raw), end}
	}
	return keys, spans, nil
}

// sameSettings compares two settings documents by meaning: same keys, and per
// key the same JSON once whitespace is removed. Claude Code writes its file in
// insertion order, so a byte comparison would rewrite it on every apply.
func sameSettings(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || compactJSON(av) != compactJSON(bv) {
			return false
		}
	}
	return true
}

func compactJSON(v json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, v); err != nil {
		return string(v)
	}
	return buf.String()
}
