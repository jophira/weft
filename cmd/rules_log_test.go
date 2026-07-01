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

// logRec builds a record with the given labels for table/filter tests.
func logRec(ts time.Time, profile, hash string, labels ...string) rules.ResolveRecord {
	loaded := make([]rules.RecordEntry, 0, len(labels))
	for _, l := range labels {
		loaded = append(loaded, rules.RecordEntry{Label: l})
	}
	return rules.ResolveRecord{Timestamp: ts, Profile: profile, ResolutionHash: hash, Loaded: loaded}
}

func TestFilterRecords_ByLabel(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	recs := []rules.ResolveRecord{
		logRec(base, "work", "aaaa", "common", "java", "springboot"),
		logRec(base, "work", "bbbb", "common", "vue"),
	}

	got := filterRecords(recs, "spring")
	if len(got) != 1 || got[0].ResolutionHash != "aaaa" {
		t.Fatalf("expected only the springboot record, got %+v", got)
	}
	// Case-insensitive.
	if len(filterRecords(recs, "VUE")) != 1 {
		t.Error("filter should be case-insensitive")
	}
	// Empty filter keeps everything.
	if len(filterRecords(recs, "")) != 2 {
		t.Error("empty filter should keep all records")
	}
}

func TestLastN(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	recs := []rules.ResolveRecord{
		logRec(base, "p", "1"), logRec(base, "p", "2"), logRec(base, "p", "3"),
	}
	if got := lastN(recs, 2); len(got) != 2 || got[0].ResolutionHash != "2" {
		t.Errorf("lastN(2) = %+v", got)
	}
	if got := lastN(recs, 0); len(got) != 3 {
		t.Error("lastN(0) should return all")
	}
	if got := lastN(recs, 10); len(got) != 3 {
		t.Error("lastN(>len) should return all")
	}
}

func TestWriteRecordsTable(t *testing.T) {
	base := time.Date(2026, 7, 1, 6, 30, 0, 0, time.UTC)
	var buf bytes.Buffer
	err := writeRecordsTable(&buf, []rules.ResolveRecord{
		logRec(base, "work", "abcdef0123456789", "common", "go"),
		logRec(base, "", "deadbeef00000000"), // empty profile + no labels → dashes
	})
	if err != nil {
		t.Fatalf("writeRecordsTable: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"TIME", "PROFILE", "HASH", "LABELS", "abcdef01", "common, go", "2 record(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "abcdef0123456789") {
		t.Error("hash should be truncated in the table")
	}
}

func TestWriteRecordsTable_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := writeRecordsTable(&buf, nil); err != nil {
		t.Fatalf("writeRecordsTable: %v", err)
	}
	if !strings.Contains(buf.String(), "no resolve history") {
		t.Errorf("expected empty-history message, got %q", buf.String())
	}
}

func TestWriteRecordsJSON(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	err := writeRecordsJSON(&buf, []rules.ResolveRecord{
		logRec(base, "work", "aaaa", "common"),
		logRec(base, "work", "bbbb", "go"),
	})
	if err != nil {
		t.Fatalf("writeRecordsJSON: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON lines, got %d", len(lines))
	}
	for _, line := range lines {
		var rec rules.ResolveRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("line is not valid JSON: %v\n%s", err, line)
		}
	}
}

// TestLoadRecords_MergesAndSkipsMissing proves the read side merges multiple
// files and treats a missing one as empty (the --global multi-month case).
func TestLoadRecords_MergesAndSkipsMissing(t *testing.T) {
	dir := t.TempDir()
	jan := filepath.Join(dir, "2026-01.jsonl")
	writeFile(t, jan, mustJSONLine(t, logRec(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), "p", "jan", "common")))

	recs, err := loadRecords([]string{jan, filepath.Join(dir, "2026-02.jsonl")}) // second is absent
	if err != nil {
		t.Fatalf("loadRecords: %v", err)
	}
	if len(recs) != 1 || recs[0].ResolutionHash != "jan" {
		t.Fatalf("expected the single January record, got %+v", recs)
	}
}

func mustJSONLine(t *testing.T, rec rules.ResolveRecord) string {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data) + "\n"
}
