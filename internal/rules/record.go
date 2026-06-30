package rules

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// maxLogBytes caps a resolve log before it is rotated to "<path>.1". The dedup
// keeps logs tiny in practice; rotation is a safety valve, not a routine event.
const maxLogBytes = 5 << 20 // 5 MiB

// RecordEntry is one loaded rule in a persisted resolve record.
type RecordEntry struct {
	Source string `json:"source,omitempty"`
	Label  string `json:"label"`
}

// ResolveRecord is one durable audit line: what rules were active for a repo at
// a point in time. ResolutionHash keys on the *selection*, so identical
// resolutions across sessions collapse to a single logged line.
type ResolveRecord struct {
	Timestamp      time.Time     `json:"ts"`
	Repo           string        `json:"repo"`
	Profile        string        `json:"profile,omitempty"`
	ResolutionHash string        `json:"resolution_hash"`
	Loaded         []RecordEntry `json:"loaded"`
}

// RecordPart is one source's contribution to a combined record.
type RecordPart struct {
	Source string
	Res    Resolution
}

// NewResolveRecord builds a record from one or more per-source resolutions. The
// combined ResolutionHash folds each part's ordered (source, label, body-hash)
// triples, so it changes exactly when the selection or a loaded body changes.
func NewResolveRecord(repo, profile string, parts []RecordPart, now time.Time) ResolveRecord {
	h := sha256.New()
	var loaded []RecordEntry
	for _, p := range parts {
		for _, lr := range p.Res.Loaded {
			loaded = append(loaded, RecordEntry{Source: p.Source, Label: lr.Label})
			h.Write([]byte(p.Source))
			h.Write([]byte{0})
			h.Write([]byte(lr.Label))
			h.Write([]byte{0})
			h.Write([]byte(hashString(lr.Body)))
			h.Write([]byte{'\n'})
		}
	}
	return ResolveRecord{
		Timestamp:      now,
		Repo:           repo,
		Profile:        profile,
		ResolutionHash: hex.EncodeToString(h.Sum(nil)),
		Loaded:         loaded,
	}
}

// RecordTargets names the files a record may be written to. Empty fields are
// skipped.
type RecordTargets struct {
	RepoLog   string // append-only, deduped per-repo log
	Latest    string // current-state snapshot (overwritten)
	GlobalLog string // append-only global rollup (gets every per-repo change)
}

// PersistOutcome reports what PersistRecord wrote.
type PersistOutcome struct {
	AppendedRepo   bool
	AppendedGlobal bool
}

// PersistRecord writes rec to its targets: it appends to the per-repo log only
// when the resolution changed since the last line, mirrors that append into the
// global rollup, and always refreshes the latest snapshot.
func PersistRecord(rec ResolveRecord, t RecordTargets) (PersistOutcome, error) {
	var out PersistOutcome
	if t.RepoLog != "" {
		wrote, err := AppendRecordIfChanged(t.RepoLog, rec)
		if err != nil {
			return out, err
		}
		out.AppendedRepo = wrote
		if wrote && t.GlobalLog != "" {
			if err := appendJSONLine(t.GlobalLog, rec); err != nil {
				return out, err
			}
			out.AppendedGlobal = true
		}
	}
	if t.Latest != "" {
		if err := WriteLatest(t.Latest, rec); err != nil {
			return out, err
		}
	}
	return out, nil
}

// AppendRecordIfChanged appends rec to the JSONL log at path only when its
// ResolutionHash differs from the log's last line (or the log is empty/absent).
// Returns whether a line was written.
func AppendRecordIfChanged(path string, rec ResolveRecord) (bool, error) {
	last, err := lastRecordHash(path)
	if err != nil {
		return false, err
	}
	if last == rec.ResolutionHash {
		return false, nil
	}
	if err := rotateIfLarge(path); err != nil {
		return false, err
	}
	return true, appendJSONLine(path, rec)
}

// WriteLatest writes rec as an indented JSON snapshot, overwriting any prior one.
func WriteLatest(path string, rec ResolveRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644) //nolint:gosec // non-secret audit snapshot
}

// lastRecordHash returns the ResolutionHash of the final non-empty line of the
// log, or "" when the log is missing or empty.
func lastRecordHash(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // audit log path derived from repo/home
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var lastLine []byte
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			lastLine = append(lastLine[:0], line...)
		}
	}
	// A scan failure (e.g. a pathological newline-free line larger than the
	// buffer) should not wedge logging: degrade to "no prior hash" so the next
	// record appends — rotation then trims the oversized file.
	if sc.Err() != nil || len(lastLine) == 0 {
		return "", nil
	}
	var rec struct {
		ResolutionHash string `json:"resolution_hash"`
	}
	if err := json.Unmarshal(lastLine, &rec); err != nil {
		// A corrupt tail line should not wedge logging; treat as "no prior hash".
		return "", nil //nolint:nilerr // intentional: degrade rather than block
	}
	return rec.ResolutionHash, nil
}

// appendJSONLine appends rec as a single JSON line, creating parent dirs.
func appendJSONLine(path string, rec ResolveRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // non-secret audit log
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // append-only log; write error below is the meaningful one
	_, err = f.Write(append(data, '\n'))
	return err
}

// rotateIfLarge renames path to "<path>.1" when it exceeds maxLogBytes, keeping
// a single previous generation.
func rotateIfLarge(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < maxLogBytes {
		return nil
	}
	return os.Rename(path, path+".1")
}
