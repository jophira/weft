package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

// setupStartupWBFixture creates a minimal fixture for startupWriteBack tests.
// It returns:
//   - stagedDir: directory that mirrors what stageProfile would produce
//   - targetRoot: the harness target directory
//   - cfgDir: weft config dir (manifest lives here)
//   - srcRoot: a single source root
func setupStartupWBFixture(t *testing.T) (stagedDir, targetRoot, cfgDir, srcRoot string) {
	t.Helper()
	base := t.TempDir()
	stagedDir = filepath.Join(base, "staged")
	targetRoot = filepath.Join(base, "target")
	cfgDir = filepath.Join(base, "cfg")
	srcRoot = filepath.Join(base, "src")
	for _, d := range []string{stagedDir, targetRoot, cfgDir, srcRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return
}

// saveManifest writes a manifest file to cfgDir for harnessName and sets
// TargetRoot so harnessTargetRoot returns targetRoot.
func saveManifest(t *testing.T, cfgDir, harnessName, targetRoot string, files map[string]string, sourceFiles map[string][]string) {
	t.Helper()
	m := &manifest.Manifest{
		Harness:     harnessName,
		Profile:     "test",
		TargetRoot:  targetRoot,
		AppliedAt:   time.Now(),
		Files:       files,
		SourceFiles: sourceFiles,
	}
	if err := manifest.Save(cfgDir, m); err != nil {
		t.Fatalf("saving manifest: %v", err)
	}
}

// TestStartupWriteBack_SingleSource_WrittenBack verifies that an externally-modified
// single-source file is written back to its source root and no backup is created.
func TestStartupWriteBack_SingleSource_WrittenBack(t *testing.T) {
	stagedDir, targetRoot, cfgDir, srcRoot := setupStartupWBFixture(t)

	const originalContent = "# original rules"
	const editedContent = "# edited by claude"

	// Source has the original content.
	writeFile(t, filepath.Join(srcRoot, "CLAUDE.md"), originalContent)
	// Staged dir mirrors what merge would produce (same as original here).
	writeFile(t, filepath.Join(stagedDir, "CLAUDE.md"), originalContent)
	// Target has been externally modified.
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), editedContent)

	// Manifest records the hash of the original content.
	originalHash := manifest.HashBytes([]byte(originalContent))
	saveManifest(t, cfgDir, "claude-code", targetRoot,
		map[string]string{"CLAUDE.md": originalHash}, nil)

	srcs := []source.Source{newSource("personal", srcRoot)}
	p := &profile.Profile{
		Name:    "test",
		Sources: []string{"personal"},
		Overlay: profile.OverlayCascade,
	}

	if err := startupWriteBack(stagedDir, "claude-code", cfgDir, p, srcs); err != nil {
		t.Fatalf("startupWriteBack: %v", err)
	}

	// Source should now contain the edited content.
	if got := readFile(t, filepath.Join(srcRoot, "CLAUDE.md")); got != editedContent {
		t.Errorf("source CLAUDE.md = %q, want %q", got, editedContent)
	}

	// No backup should have been created.
	backupsDir := filepath.Join(cfgDir, "backups")
	if _, err := os.Stat(backupsDir); err == nil {
		t.Error("backups dir should not exist when write-back succeeds")
	}
}

// TestStartupWriteBack_NoManifest_Noop verifies that when no manifest exists
// (first run), startupWriteBack is a no-op.
func TestStartupWriteBack_NoManifest_Noop(t *testing.T) {
	stagedDir, _, cfgDir, srcRoot := setupStartupWBFixture(t)

	writeFile(t, filepath.Join(stagedDir, "CLAUDE.md"), "original")
	srcs := []source.Source{newSource("personal", srcRoot)}
	p := &profile.Profile{Name: "test", Sources: []string{"personal"}}

	if err := startupWriteBack(stagedDir, "claude-code", cfgDir, p, srcs); err != nil {
		t.Fatalf("startupWriteBack: %v", err)
	}
	// No writes should have occurred (source doesn't have CLAUDE.md).
	if _, err := os.Stat(filepath.Join(srcRoot, "CLAUDE.md")); err == nil {
		t.Error("source CLAUDE.md should not have been written when no manifest exists")
	}
}

// TestStartupWriteBack_UnchangedFile_Noop verifies that a file whose on-disk hash
// matches the manifest hash is not written back.
func TestStartupWriteBack_UnchangedFile_Noop(t *testing.T) {
	stagedDir, targetRoot, cfgDir, srcRoot := setupStartupWBFixture(t)

	const content = "# rules"
	writeFile(t, filepath.Join(srcRoot, "CLAUDE.md"), content)
	writeFile(t, filepath.Join(stagedDir, "CLAUDE.md"), content)
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), content)

	hash := manifest.HashBytes([]byte(content))
	saveManifest(t, cfgDir, "claude-code", targetRoot,
		map[string]string{"CLAUDE.md": hash}, nil)

	srcs := []source.Source{newSource("personal", srcRoot)}
	p := &profile.Profile{Name: "test", Sources: []string{"personal"}}

	// Track modification time before.
	srcStat, err := os.Stat(filepath.Join(srcRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	mtime := srcStat.ModTime()

	if err := startupWriteBack(stagedDir, "claude-code", cfgDir, p, srcs); err != nil {
		t.Fatalf("startupWriteBack: %v", err)
	}

	// Source file should not have been touched.
	newStat, _ := os.Stat(filepath.Join(srcRoot, "CLAUDE.md"))
	if !newStat.ModTime().Equal(mtime) {
		t.Error("source file should not have been modified when target is unchanged")
	}
}

// TestStartupWriteBack_Unresolvable_BackupAndWarn verifies that when no owning
// source can be found, the target file is backed up and the function returns nil
// (not an error — a warning is printed instead).
func TestStartupWriteBack_Unresolvable_BackupAndWarn(t *testing.T) {
	stagedDir, targetRoot, cfgDir, srcRoot := setupStartupWBFixture(t)

	const editedContent = "# user-edited but no source owns this"

	// File is in staged and target but NOT in any source root.
	writeFile(t, filepath.Join(stagedDir, "orphan.md"), "original orphan")
	writeFile(t, filepath.Join(targetRoot, "orphan.md"), editedContent)

	originalHash := manifest.HashBytes([]byte("original orphan"))
	saveManifest(t, cfgDir, "claude-code", targetRoot,
		map[string]string{"orphan.md": originalHash}, nil)

	srcs := []source.Source{newSource("personal", srcRoot)}
	// No write_back.default — owning source cannot be resolved.
	p := &profile.Profile{
		Name:    "test",
		Sources: []string{"personal"},
		Overlay: profile.OverlayCascade,
	}

	if err := startupWriteBack(stagedDir, "claude-code", cfgDir, p, srcs); err != nil {
		t.Fatalf("startupWriteBack returned error for unresolvable file: %v", err)
	}

	// A backup should have been created.
	backupsDir := filepath.Join(cfgDir, "backups", "claude-code")
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		t.Fatalf("expected backups dir to exist: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one backup timestamp dir")
	}

	// Read the backed-up file.
	tsDir := entries[0].Name()
	backed := readFile(t, filepath.Join(backupsDir, tsDir, "orphan.md"))
	if backed != editedContent {
		t.Errorf("backup content = %q, want %q", backed, editedContent)
	}

	// Source should NOT have been written.
	if _, err := os.Stat(filepath.Join(srcRoot, "orphan.md")); err == nil {
		t.Error("source should not have been written for unresolvable file")
	}
}

// TestStartupWriteBack_MultiSource_WrittenBack verifies that a multi-source file
// (OverlayMerge) that has been externally modified is written back via
// writeBackMergedSource.
func TestStartupWriteBack_MultiSource_WrittenBack(t *testing.T) {
	stagedDir, targetRoot, cfgDir, _ := setupStartupWBFixture(t)

	// Two source roots.
	srcARoot := t.TempDir()
	srcBRoot := t.TempDir()

	// Source A provides section A; source B provides section B.
	srcAContent := "# Section A\nwork rules\n"
	srcBContent := "# Section B\npersonal rules\n"
	merged := srcAContent + srcBContent

	writeFile(t, filepath.Join(srcARoot, "CLAUDE.md"), srcAContent)
	writeFile(t, filepath.Join(srcBRoot, "CLAUDE.md"), srcBContent)
	writeFile(t, filepath.Join(stagedDir, "CLAUDE.md"), merged)

	// Target has been edited: add a line to section A.
	editedTarget := "# Section A\nwork rules\nextra line\n" + srcBContent
	writeFile(t, filepath.Join(targetRoot, "CLAUDE.md"), editedTarget)

	originalHash := manifest.HashBytes([]byte(merged))
	saveManifest(t, cfgDir, "claude-code", targetRoot,
		map[string]string{"CLAUDE.md": originalHash},
		map[string][]string{"CLAUDE.md": {"work", "personal"}})

	srcs := []source.Source{newSource("work", srcARoot), newSource("personal", srcBRoot)}
	p := &profile.Profile{
		Name:    "test",
		Sources: []string{"work", "personal"},
		Overlay: profile.OverlayMerge,
	}

	if err := startupWriteBack(stagedDir, "claude-code", cfgDir, p, srcs); err != nil {
		t.Fatalf("startupWriteBack: %v", err)
	}

	// Source A should include the extra line.
	srcAUpdated := readFile(t, filepath.Join(srcARoot, "CLAUDE.md"))
	if !strings.Contains(srcAUpdated, "extra line") {
		t.Errorf("source A CLAUDE.md should contain 'extra line', got: %q", srcAUpdated)
	}
	// Source B should be unchanged (no edits in its section).
	srcBUpdated := readFile(t, filepath.Join(srcBRoot, "CLAUDE.md"))
	if srcBUpdated != srcBContent {
		t.Errorf("source B CLAUDE.md should be unchanged, got: %q", srcBUpdated)
	}

	// No backup should have been created.
	if _, err := os.Stat(filepath.Join(cfgDir, "backups")); err == nil {
		t.Error("backups dir should not exist when merged write-back succeeds")
	}
}

// TestStartupWriteBack_FileNotOnDisk_Noop verifies that files present in the
// staged dir but not yet on disk in the target are ignored.
func TestStartupWriteBack_FileNotOnDisk_Noop(t *testing.T) {
	stagedDir, targetRoot, cfgDir, srcRoot := setupStartupWBFixture(t)

	writeFile(t, filepath.Join(stagedDir, "new-file.md"), "new content")
	// Target does NOT have new-file.md.

	saveManifest(t, cfgDir, "claude-code", targetRoot,
		map[string]string{}, nil)

	srcs := []source.Source{newSource("personal", srcRoot)}
	p := &profile.Profile{Name: "test", Sources: []string{"personal"}}

	if err := startupWriteBack(stagedDir, "claude-code", cfgDir, p, srcs); err != nil {
		t.Fatalf("startupWriteBack: %v", err)
	}
	// Source should not have new-file.md.
	if _, err := os.Stat(filepath.Join(srcRoot, "new-file.md")); err == nil {
		t.Error("source should not have been written for file absent from target")
	}
}

// TestResolvedSourceName_SingleSource returns the owning source name.
func TestResolvedSourceName_SingleSource(t *testing.T) {
	srcRoot := t.TempDir()
	writeFile(t, filepath.Join(srcRoot, "CLAUDE.md"), "content")

	srcs := []source.Source{newSource("personal", srcRoot)}
	p := &profile.Profile{Name: "test", Sources: []string{"personal"}}
	m := &manifest.Manifest{Files: map[string]string{}}

	name := resolvedSourceName("CLAUDE.md", p, srcs, m)
	if name != "personal" {
		t.Errorf("resolvedSourceName = %q, want %q", name, "personal")
	}
}

// TestResolvedSourceName_MultiSource returns joined source names.
func TestResolvedSourceName_MultiSource(t *testing.T) {
	srcs := []source.Source{newSource("work", t.TempDir()), newSource("personal", t.TempDir())}
	p := &profile.Profile{Name: "test", Sources: []string{"work", "personal"}}
	m := &manifest.Manifest{
		Files: map[string]string{},
		SourceFiles: map[string][]string{
			"CLAUDE.md": {"work", "personal"},
		},
	}

	name := resolvedSourceName("CLAUDE.md", p, srcs, m)
	if name != "work+personal" {
		t.Errorf("resolvedSourceName = %q, want %q", name, "work+personal")
	}
}
