package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func recNow() time.Time { return time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC) }

func partFor(source string, labels ...string) RecordPart {
	loaded := make([]LoadedRule, 0, len(labels))
	for _, l := range labels {
		loaded = append(loaded, LoadedRule{Label: l, Body: l + " body"})
	}
	return RecordPart{Source: source, Res: Resolution{Loaded: loaded}}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func TestNewResolveRecord_HashSelective(t *testing.T) {
	a := NewResolveRecord("/repo", "p", []RecordPart{partFor("s", "common", "java")}, recNow())
	b := NewResolveRecord("/repo", "p", []RecordPart{partFor("s", "common", "java")}, recNow().Add(time.Hour))
	if a.ResolutionHash != b.ResolutionHash {
		t.Error("same selection at different times must share a hash")
	}
	c := NewResolveRecord("/repo", "p", []RecordPart{partFor("s", "common")}, recNow())
	if a.ResolutionHash == c.ResolutionHash {
		t.Error("different selections must differ in hash")
	}
	if len(a.Loaded) != 2 || a.Loaded[0].Label != "common" || a.Loaded[0].Source != "s" {
		t.Errorf("unexpected loaded entries: %+v", a.Loaded)
	}
}

func TestAppendRecordIfChanged_Dedup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolve.log.jsonl")
	rec := NewResolveRecord("/repo", "", []RecordPart{partFor("", "common")}, recNow())

	wrote, err := AppendRecordIfChanged(path, rec)
	if err != nil || !wrote {
		t.Fatalf("first append: wrote=%v err=%v", wrote, err)
	}
	// Identical resolution → no new line.
	wrote, err = AppendRecordIfChanged(path, rec)
	if err != nil || wrote {
		t.Fatalf("dedup append: wrote=%v err=%v", wrote, err)
	}
	if n := countLines(t, path); n != 1 {
		t.Errorf("expected 1 line after dedup, got %d", n)
	}
	// Changed resolution → appends.
	rec2 := NewResolveRecord("/repo", "", []RecordPart{partFor("", "common", "java")}, recNow())
	if wrote, _ := AppendRecordIfChanged(path, rec2); !wrote {
		t.Error("changed resolution should append")
	}
	if n := countLines(t, path); n != 2 {
		t.Errorf("expected 2 lines, got %d", n)
	}
}

func TestPersistRecord_RepoGlobalLatest(t *testing.T) {
	dir := t.TempDir()
	targets := RecordTargets{
		RepoLog:   filepath.Join(dir, "repo", ".weft", "resolve.log.jsonl"),
		Latest:    filepath.Join(dir, "repo", ".weft", "resolve.latest.json"),
		GlobalLog: filepath.Join(dir, "home", ".weft", "audit", "2026-06.jsonl"),
	}
	rec := NewResolveRecord("/repo", "p", []RecordPart{partFor("s", "common")}, recNow())

	out, err := PersistRecord(rec, targets)
	if err != nil {
		t.Fatalf("PersistRecord: %v", err)
	}
	if !out.AppendedRepo || !out.AppendedGlobal {
		t.Errorf("expected repo+global appended, got %+v", out)
	}
	if countLines(t, targets.RepoLog) != 1 || countLines(t, targets.GlobalLog) != 1 {
		t.Error("expected one line in repo log and global rollup")
	}
	if _, err := os.Stat(targets.Latest); err != nil {
		t.Errorf("latest snapshot missing: %v", err)
	}

	// Identical resolve: no new repo/global lines, but latest still rewritten.
	out, err = PersistRecord(rec, targets)
	if err != nil {
		t.Fatalf("PersistRecord 2: %v", err)
	}
	if out.AppendedRepo || out.AppendedGlobal {
		t.Errorf("identical resolve must not append, got %+v", out)
	}
	if countLines(t, targets.RepoLog) != 1 || countLines(t, targets.GlobalLog) != 1 {
		t.Error("logs should not grow on identical resolve")
	}
}

func TestRotateIfLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.jsonl")
	big := make([]byte, maxLogBytes+1)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	rec := NewResolveRecord("/repo", "", []RecordPart{partFor("", "x")}, recNow())
	if _, err := AppendRecordIfChanged(path, rec); err != nil {
		t.Fatalf("append after large: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("expected rotated file %s.1: %v", path, err)
	}
	if n := countLines(t, path); n != 1 {
		t.Errorf("rotated current log should hold the new line only, got %d", n)
	}
}

func TestLastRecordHash_CorruptTailIsTolerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolve.log.jsonl")
	if err := os.WriteFile(path, []byte("{not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := lastRecordHash(path)
	if err != nil {
		t.Fatalf("corrupt tail should not error: %v", err)
	}
	if h != "" {
		t.Errorf("expected empty hash for corrupt tail, got %q", h)
	}
}
