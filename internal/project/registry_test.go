package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func at(day int) time.Time {
	return time.Date(2026, 8, day, 12, 0, 0, 0, time.UTC)
}

// alwaysExists is the Prune predicate for tests that are not about dead roots.
func alwaysExists(string) bool { return true }

func TestLoad_missingFileIsEmptyNotAnError(t *testing.T) {
	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load on a fresh dir: %v", err)
	}
	if len(r.Projects) != 0 {
		t.Errorf("Projects = %d, want 0", len(r.Projects))
	}
}

func TestSaveLoad_roundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Project{
		Root:     "/repo/one",
		Repo:     "one",
		Remote:   "git@github.com:acme/one.git",
		Profile:  "hybrid",
		LastSeen: at(16),
		Enabled:  true,
	}
	if err := Save(dir, &Registry{Projects: []Project{want}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Projects) != 1 {
		t.Fatalf("Projects = %d, want 1", len(got.Projects))
	}
	p := got.Projects[0]
	if p.Root != want.Root || p.Repo != want.Repo || p.Remote != want.Remote || p.Profile != want.Profile || !p.Enabled {
		t.Errorf("round trip = %+v, want %+v", p, want)
	}
	if !p.LastSeen.Equal(want.LastSeen) {
		t.Errorf("LastSeen = %v, want %v", p.LastSeen, want.LastSeen)
	}
}

func TestUpsert_newEntryReportsChanged(t *testing.T) {
	r := &Registry{}
	if !r.Upsert(Project{Root: "/repo/one", Repo: "one", LastSeen: at(16), Enabled: true}) {
		t.Error("Upsert of a new root reported no change")
	}
	if len(r.Projects) != 1 {
		t.Errorf("Projects = %d, want 1", len(r.Projects))
	}
}

func TestUpsert_repeatVisitIsNotAChange(t *testing.T) {
	r := &Registry{}
	r.Upsert(Project{Root: "/repo/one", Repo: "one", LastSeen: at(16), Enabled: true})

	// A statusline calling weft every second must not rewrite the file every
	// second, so an unchanged visit reports false even though LastSeen moves.
	if r.Upsert(Project{Root: "/repo/one", Repo: "one", LastSeen: at(17), Enabled: true}) {
		t.Error("Upsert with identical details reported a change")
	}
	if got := r.Get("/repo/one").LastSeen; !got.Equal(at(17)) {
		t.Errorf("LastSeen = %v, want it refreshed to %v", got, at(17))
	}
}

func TestUpsert_renamedRemoteUpdatesInPlace(t *testing.T) {
	r := &Registry{}
	r.Upsert(Project{Root: "/repo/one", Repo: "one", Remote: "git@github.com:acme/one.git", LastSeen: at(16), Enabled: true})

	// The root is the key, so a renamed remote updates the attribute rather than
	// creating a second entry for the same working tree.
	if !r.Upsert(Project{Root: "/repo/one", Repo: "one", Remote: "git@github.com:acme/renamed.git", LastSeen: at(17), Enabled: true}) {
		t.Error("Upsert with a new remote reported no change")
	}
	if len(r.Projects) != 1 {
		t.Fatalf("Projects = %d, want 1 — a rename must not duplicate the entry", len(r.Projects))
	}
	if got := r.Get("/repo/one").Remote; got != "git@github.com:acme/renamed.git" {
		t.Errorf("Remote = %q, want the renamed value", got)
	}
}

func TestUpsert_twoClonesOfOneRepoAreTwoEntries(t *testing.T) {
	r := &Registry{}
	remote := "git@github.com:acme/one.git"
	r.Upsert(Project{Root: "/repo/one", Repo: "one", Remote: remote, LastSeen: at(16), Enabled: true})
	r.Upsert(Project{Root: "/work/263_thing", Repo: "263_thing", Remote: remote, LastSeen: at(16), Enabled: true})

	// Two working trees hold two sets of files, so both need delivery.
	if len(r.Projects) != 2 {
		t.Errorf("Projects = %d, want 2 for two checkouts of one repository", len(r.Projects))
	}
}

func TestPrune_dropsStaleAndDeadRoots(t *testing.T) {
	r := &Registry{Projects: []Project{
		{Root: "/live", Repo: "live", LastSeen: at(16), Enabled: true},
		{Root: "/stale", Repo: "stale", LastSeen: at(1), Enabled: true},
		{Root: "/deleted", Repo: "deleted", LastSeen: at(16), Enabled: true},
	}}
	exists := func(p string) bool { return p != "/deleted" }

	dropped := r.Prune(at(16), 5*24*time.Hour, exists)

	if len(dropped) != 2 {
		t.Fatalf("dropped %d, want 2", len(dropped))
	}
	if len(r.Projects) != 1 || r.Projects[0].Root != "/live" {
		t.Errorf("kept %+v, want only /live", r.Projects)
	}
}

func TestPrune_neverVisitedCountsAsStale(t *testing.T) {
	r := &Registry{Projects: []Project{{Root: "/x", Repo: "x", Enabled: true}}}
	if dropped := r.Prune(at(16), DefaultMaxAge, alwaysExists); len(dropped) != 1 {
		t.Errorf("dropped %d, want 1 — a zero LastSeen is stale", len(dropped))
	}
}

func TestActive_excludesDisabledAndStale(t *testing.T) {
	r := &Registry{Projects: []Project{
		{Root: "/on", LastSeen: at(16), Enabled: true},
		{Root: "/off", LastSeen: at(16), Enabled: false},
		{Root: "/old", LastSeen: at(1), Enabled: true},
	}}
	got := r.Active(at(16), 5*24*time.Hour)
	if len(got) != 1 || got[0].Root != "/on" {
		t.Errorf("Active = %+v, want only /on", got)
	}
}

func TestForget_removesAndReports(t *testing.T) {
	r := &Registry{Projects: []Project{{Root: "/a"}, {Root: "/b"}}}
	if !r.Forget("/a") {
		t.Error("Forget reported nothing removed for a present root")
	}
	if r.Forget("/missing") {
		t.Error("Forget reported a removal for an absent root")
	}
	if len(r.Projects) != 1 || r.Projects[0].Root != "/b" {
		t.Errorf("remaining = %+v, want only /b", r.Projects)
	}
}

func TestSave_isAtomicallyReplaced(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, &Registry{Projects: []Project{{Root: "/a", LastSeen: at(16)}}}); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if err := Save(dir, &Registry{Projects: []Project{{Root: "/b", LastSeen: at(16)}}}); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	// No temp files may survive; the watcher and the CLI both write here.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != RegistryFileName {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("dir contains %v, want only %s", names, RegistryFileName)
	}
}

func TestLoad_corruptFileIsReported(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RegistryFileName), []byte("projects: [oh dear"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("Load of a corrupt registry returned no error")
	}
}
