package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// buildSourceTree creates a temporary directory tree from a map of rel-path → content.
func buildSourceTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("buildSourceTree mkdir %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("buildSourceTree write %s: %v", abs, err)
		}
	}
	return dir
}

// newFilledSource returns a source.Source rooted at a temp dir built from files.
func newFilledSource(t *testing.T, name string, files map[string]string) source.Source {
	t.Helper()
	return source.Source{
		Name:      name,
		Root:      buildSourceTree(t, files),
		Structure: source.DefaultStructure(),
	}
}

// ─── replaceSourcesBlock ──────────────────────────────────────────────────────

func TestReplaceSourcesBlock_ReplacesContent(t *testing.T) {
	content := "before\n" + sourcesBegin + "\nold content\n" + sourcesEnd + "\nafter"
	got := replaceSourcesBlock(content, "NEW")
	if strings.Contains(got, "old content") {
		t.Error("old content should be removed")
	}
	if !strings.Contains(got, "NEW") {
		t.Error("replacement should appear in result")
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Error("surrounding content should be preserved")
	}
}

func TestReplaceSourcesBlock_MissingBegin(t *testing.T) {
	content := "no markers here"
	got := replaceSourcesBlock(content, "NEW")
	if got != content {
		t.Error("content should be unchanged when begin marker is absent")
	}
}

func TestReplaceSourcesBlock_MissingEnd(t *testing.T) {
	content := sourcesBegin + "\nno end marker"
	got := replaceSourcesBlock(content, "NEW")
	if got != content {
		t.Error("content should be unchanged when end marker is absent")
	}
}

// ─── classifySourceFile ───────────────────────────────────────────────────────

func TestClassifySourceFile_RootFile(t *testing.T) {
	if got := classifySourceFile("CLAUDE.md"); got != "" {
		t.Errorf("root file should always load, got %q", got)
	}
}

func TestClassifySourceFile_CommonDir(t *testing.T) {
	cases := []string{
		filepath.Join("dev", "common.md"),
		filepath.Join("dev", "common-backend.md"),
		filepath.Join("dev", "common", "extra.md"),
	}
	for _, c := range cases {
		if got := classifySourceFile(c); got != "" {
			t.Errorf("classifySourceFile(%q) = %q, want always-load", c, got)
		}
	}
}

func TestClassifySourceFile_DocDir(t *testing.T) {
	if got := classifySourceFile(filepath.Join("dev", "doc", "doc.md")); got != "" {
		t.Errorf("dev/doc/ should always load, got %q", got)
	}
}

func TestClassifySourceFile_KnownStackGo(t *testing.T) {
	want := knownStacks["go"].label
	got := classifySourceFile(filepath.Join("dev", "go", "go.md"))
	if got != want {
		t.Errorf("classifySourceFile(dev/go/go.md) = %q, want %q", got, want)
	}
}

func TestClassifySourceFile_KnownStackJava(t *testing.T) {
	want := knownStacks["java"].label
	got := classifySourceFile(filepath.Join("dev", "java", "java.md"))
	if got != want {
		t.Errorf("classifySourceFile(dev/java/java.md) = %q, want %q", got, want)
	}
}

func TestClassifySourceFile_KnownStackPython(t *testing.T) {
	want := knownStacks["python"].label
	got := classifySourceFile(filepath.Join("dev", "python", "python.md"))
	if got != want {
		t.Errorf("classifySourceFile(dev/python/python.md) = %q, want %q", got, want)
	}
}

func TestClassifySourceFile_KnownStackVue(t *testing.T) {
	want := knownStacks["vue"].label
	got := classifySourceFile(filepath.Join("dev", "vue", "vue.md"))
	if got != want {
		t.Errorf("classifySourceFile(dev/vue/vue.md) = %q, want %q", got, want)
	}
}

func TestClassifySourceFile_KnownStackRust(t *testing.T) {
	want := knownStacks["rust"].label
	got := classifySourceFile(filepath.Join("dev", "rust", "rust.md"))
	if got != want {
		t.Errorf("classifySourceFile(dev/rust/rust.md) = %q, want %q", got, want)
	}
}

func TestClassifySourceFile_UnknownStackDir(t *testing.T) {
	if got := classifySourceFile(filepath.Join("dev", "myframework", "rules.md")); got != "" {
		t.Errorf("unknown stack dir should always load, got %q", got)
	}
}

func TestClassifySourceFile_NonDevTopLevelDir(t *testing.T) {
	if got := classifySourceFile(filepath.Join("guidelines", "rules.md")); got != "" {
		t.Errorf("non-dev dir should always load, got %q", got)
	}
}

func TestClassifySourceFile_NestedUnderKnownStack(t *testing.T) {
	// Files nested multiple levels under a known stack inherit the condition.
	want := knownStacks["go"].label
	got := classifySourceFile(filepath.Join("dev", "go", "sub", "extra.md"))
	if got != want {
		t.Errorf("nested file under dev/go/ should inherit go condition, got %q", got)
	}
}

// ─── collectSourceFiles ───────────────────────────────────────────────────────

func TestCollectSourceFiles_RootOnly(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"CLAUDE.md": "root rules",
	})
	files, err := collectSourceFiles(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].condition != "" {
		t.Errorf("root CLAUDE.md should always load, got condition %q", files[0].condition)
	}
}

func TestCollectSourceFiles_DevStackDirs(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"CLAUDE.md":        "root",
		"dev/go/go.md":     "go rules",
		"dev/java/java.md": "java rules",
		"dev/common.md":    "common rules",
		"dev/doc/doc.md":   "doc rules",
	})
	files, err := collectSourceFiles(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	byRel := map[string]string{}
	for _, f := range files {
		rel, _ := filepath.Rel(root, f.abs)
		byRel[filepath.ToSlash(rel)] = f.condition
	}

	cases := map[string]string{
		"CLAUDE.md":        "",
		"dev/common.md":    "",
		"dev/doc/doc.md":   "",
		"dev/go/go.md":     knownStacks["go"].label,
		"dev/java/java.md": knownStacks["java"].label,
	}
	for rel, wantCond := range cases {
		got, ok := byRel[rel]
		if !ok {
			t.Errorf("expected file %q to be discovered", rel)
			continue
		}
		if got != wantCond {
			t.Errorf("file %q: condition = %q, want %q", rel, got, wantCond)
		}
	}
}

func TestCollectSourceFiles_ExcludesManagedDirs(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"CLAUDE.md":                "root",
		"commands/deploy.md":       "deploy",
		"skills/graphify/SKILL.md": "skill",
		"hooks/pre-commit.md":      "hook",
		"agents/go.md":             "agent",
		"memory/notes.md":          "memory",
	})
	managedDirs := []string{"commands", "skills", "hooks", "agents", "memory"}
	files, err := collectSourceFiles(root, managedDirs, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		rel, _ := filepath.Rel(root, f.abs)
		for _, m := range managedDirs {
			if strings.HasPrefix(filepath.ToSlash(rel), m+"/") {
				t.Errorf("managed dir file should be excluded: %s", rel)
			}
		}
	}
	// CLAUDE.md at root must still be present.
	found := false
	for _, f := range files {
		if strings.HasSuffix(f.abs, "CLAUDE.md") {
			found = true
		}
	}
	if !found {
		t.Error("expected CLAUDE.md to be retained after managed-dir exclusion")
	}
}

func TestCollectSourceFiles_ExcludesProjectDirs(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"CLAUDE.md":               "root",
		"dev/go/go.md":            "go rules",
		"dev/go/projects/weft.md": "weft project",
		"projects/myapp.md":       "project rules",
		"project-rules/other.md":  "other project",
	})
	projectDirs := buildNameSet([]string{"projects", "project-rules"})
	files, err := collectSourceFiles(root, nil, projectDirs)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		rel, _ := filepath.Rel(root, f.abs)
		if strings.Contains(filepath.ToSlash(rel), "projects/") ||
			strings.Contains(filepath.ToSlash(rel), "project-rules/") {
			t.Errorf("project dir file should be excluded: %s", rel)
		}
	}
	// dev/go/go.md must still be found.
	found := false
	for _, f := range files {
		if strings.HasSuffix(filepath.ToSlash(f.abs), "dev/go/go.md") {
			found = true
		}
	}
	if !found {
		t.Error("expected dev/go/go.md to remain after project-dir exclusion")
	}
}

func TestCollectSourceFiles_ExcludesHiddenFiles(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"CLAUDE.md":       "root",
		".hidden.md":      "hidden file",
		".hiddendir/a.md": "hidden dir file",
		"dev/.secret.md":  "secret",
	})
	files, err := collectSourceFiles(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(f.abs, ".hidden") || strings.Contains(f.abs, ".secret") {
			t.Errorf("hidden file/dir should be excluded: %s", f.abs)
		}
	}
}

func TestCollectSourceFiles_ExcludesNonMarkdown(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"CLAUDE.md":         "root",
		"readme.txt":        "not md",
		"dev/go/go.go":      "go source code",
		"dev/go/schema.sql": "sql",
	})
	files, err := collectSourceFiles(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if filepath.Ext(f.abs) != ".md" {
			t.Errorf("non-.md file should be excluded: %s", f.abs)
		}
	}
}

func TestCollectSourceFiles_NonexistentRoot(t *testing.T) {
	files, err := collectSourceFiles("/nonexistent/path/xyz", nil, nil)
	if err != nil {
		t.Fatalf("nonexistent root should not return error, got: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected empty result for nonexistent root, got: %v", files)
	}
}

func TestCollectSourceFiles_DeepNesting(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"CLAUDE.md":                 "root",
		"dev/go/sub/deep/nested.md": "deep go rules",
	})
	files, err := collectSourceFiles(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		rel, _ := filepath.Rel(root, f.abs)
		if filepath.ToSlash(rel) == "dev/go/sub/deep/nested.md" {
			found = true
			if f.condition != knownStacks["go"].label {
				t.Errorf("deep file under dev/go/ should have go condition, got %q", f.condition)
			}
		}
	}
	if !found {
		t.Error("expected dev/go/sub/deep/nested.md to be discovered")
	}
}

func TestCollectSourceFiles_MultipleStacksAndAlways(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"CLAUDE.md":            "root",
		"dev/common.md":        "common",
		"dev/doc/doc.md":       "doc",
		"dev/go/go.md":         "go",
		"dev/python/python.md": "python",
		"dev/vue/vue.md":       "vue",
	})
	files, err := collectSourceFiles(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	byRel := map[string]string{}
	for _, f := range files {
		rel, _ := filepath.Rel(root, f.abs)
		byRel[filepath.ToSlash(rel)] = f.condition
	}

	alwaysLoad := []string{"CLAUDE.md", "dev/common.md", "dev/doc/doc.md"}
	for _, rel := range alwaysLoad {
		if cond, ok := byRel[rel]; !ok || cond != "" {
			t.Errorf("file %q should always load, got condition %q (found=%v)", rel, cond, ok)
		}
	}

	conditional := map[string]string{
		"dev/go/go.md":         knownStacks["go"].label,
		"dev/python/python.md": knownStacks["python"].label,
		"dev/vue/vue.md":       knownStacks["vue"].label,
	}
	for rel, wantCond := range conditional {
		if cond, ok := byRel[rel]; !ok || cond != wantCond {
			t.Errorf("file %q: condition = %q, want %q (found=%v)", rel, cond, wantCond, ok)
		}
	}
}

// ─── generateSourcesSnippet ───────────────────────────────────────────────────

func TestGenerateSourcesSnippet_EmptyNoSources(t *testing.T) {
	snippet := generateSourcesSnippet(nil, nil)
	if !strings.Contains(snippet, sourcesBegin) || !strings.Contains(snippet, sourcesEnd) {
		t.Error("empty snippet should still contain begin/end markers")
	}
	// No file paths.
	inner := strings.TrimPrefix(strings.TrimSuffix(snippet, sourcesEnd), sourcesBegin)
	if strings.Contains(inner, "`") {
		t.Errorf("expected no file paths in empty snippet, got: %q", inner)
	}
}

func TestGenerateSourcesSnippet_EmptySourceRoot(t *testing.T) {
	src := source.Source{
		Name:      "empty",
		Root:      t.TempDir(), // no files at all
		Structure: source.DefaultStructure(),
	}
	snippet := generateSourcesSnippet([]source.Source{src}, nil)
	if !strings.Contains(snippet, sourcesBegin) || !strings.Contains(snippet, sourcesEnd) {
		t.Error("snippet should contain begin/end markers even for empty source")
	}
}

func TestGenerateSourcesSnippet_AlwaysLoadOnly(t *testing.T) {
	src := newFilledSource(t, "ai-rules", map[string]string{
		"CLAUDE.md":     "root",
		"dev/common.md": "common",
		"dev/doc/a.md":  "doc",
	})
	snippet := generateSourcesSnippet([]source.Source{src}, nil)

	if !strings.Contains(snippet, "Always read:") {
		t.Error("expected 'Always read:' section")
	}
	if strings.Contains(snippet, "also read:") {
		t.Error("unexpected conditional section for always-load-only source")
	}
}

func TestGenerateSourcesSnippet_ConditionalLoading(t *testing.T) {
	src := newFilledSource(t, "ai-rules", map[string]string{
		"CLAUDE.md":        "root",
		"dev/go/go.md":     "go",
		"dev/java/java.md": "java",
		"dev/python/py.md": "python",
	})
	snippet := generateSourcesSnippet([]source.Source{src}, nil)

	for stack, cond := range map[string]string{
		"go":     knownStacks["go"].label,
		"java":   knownStacks["java"].label,
		"python": knownStacks["python"].label,
	} {
		if !strings.Contains(snippet, cond) {
			t.Errorf("expected %s condition %q in snippet", stack, cond)
		}
	}
	if !strings.Contains(snippet, "also read:") {
		t.Error("expected 'also read:' in conditional section")
	}
}

func TestGenerateSourcesSnippet_ConditionalGroupsSorted(t *testing.T) {
	src := newFilledSource(t, "ai-rules", map[string]string{
		"dev/go/go.md":         "go",
		"dev/java/java.md":     "java",
		"dev/python/python.md": "python",
	})
	snippet := generateSourcesSnippet([]source.Source{src}, nil)

	gIdx := strings.Index(snippet, knownStacks["go"].label)
	jIdx := strings.Index(snippet, knownStacks["java"].label)
	pIdx := strings.Index(snippet, knownStacks["python"].label)
	if gIdx < 0 || jIdx < 0 || pIdx < 0 {
		t.Fatal("expected all three stack conditions in snippet")
	}
	// go < java < python alphabetically.
	if gIdx >= jIdx || jIdx >= pIdx {
		t.Errorf("conditions should be sorted alphabetically (go=%d java=%d python=%d)", gIdx, jIdx, pIdx)
	}
}

func TestGenerateSourcesSnippet_PrimarySourceFromWriteBack(t *testing.T) {
	src := newFilledSource(t, "my-source", map[string]string{
		"CLAUDE.md": "root",
	})
	p := &profile.Profile{
		WriteBack: profile.WriteBack{Default: "my-source"},
	}
	snippet := generateSourcesSnippet([]source.Source{src}, p)

	if !strings.Contains(snippet, "Primary source for edits:") {
		t.Error("expected primary source write-back instruction")
	}
	if !strings.Contains(snippet, src.Root) {
		t.Errorf("expected source root path in write-back instruction; snippet:\n%s", snippet)
	}
}

func TestGenerateSourcesSnippet_PrimarySourceFallsBackToFirst(t *testing.T) {
	src := newFilledSource(t, "first-source", map[string]string{
		"CLAUDE.md": "root",
	})
	// WriteBack.Default is empty — should fall back to first source.
	snippet := generateSourcesSnippet([]source.Source{src}, &profile.Profile{})

	if !strings.Contains(snippet, "Primary source for edits:") {
		t.Error("expected primary source fallback to first source")
	}
}

func TestGenerateSourcesSnippet_PrimarySourceUnknownName(t *testing.T) {
	src := newFilledSource(t, "actual-source", map[string]string{
		"CLAUDE.md": "root",
	})
	// WriteBack.Default names a source that doesn't exist — falls back to first.
	p := &profile.Profile{
		WriteBack: profile.WriteBack{Default: "nonexistent"},
	}
	snippet := generateSourcesSnippet([]source.Source{src}, p)

	if !strings.Contains(snippet, "Primary source for edits:") {
		t.Error("expected fallback to first source when named source not found")
	}
}

func TestGenerateSourcesSnippet_MultipleSources(t *testing.T) {
	src1 := newFilledSource(t, "work", map[string]string{
		"CLAUDE.md":    "root work",
		"dev/go/go.md": "go",
	})
	src2 := newFilledSource(t, "personal", map[string]string{
		"CLAUDE.md":        "root personal",
		"dev/java/java.md": "java",
	})
	snippet := generateSourcesSnippet([]source.Source{src1, src2}, nil)

	if !strings.Contains(snippet, knownStacks["go"].label) {
		t.Error("expected go condition from first source")
	}
	if !strings.Contains(snippet, knownStacks["java"].label) {
		t.Error("expected java condition from second source")
	}
}

func TestGenerateSourcesSnippet_ExcludesProjectDirFiles(t *testing.T) {
	src := newFilledSource(t, "ai-rules", map[string]string{
		"CLAUDE.md":         "root",
		"projects/myapp.md": "project rules",
	})
	snippet := generateSourcesSnippet([]source.Source{src}, nil)

	if strings.Contains(snippet, "myapp.md") {
		t.Error("project dir files should not appear in sources snippet")
	}
}

func TestGenerateSourcesSnippet_ExcludesManagedDirFiles(t *testing.T) {
	src := newFilledSource(t, "ai-rules", map[string]string{
		"CLAUDE.md":           "root",
		"commands/deploy.md":  "command",
		"skills/foo/SKILL.md": "skill",
		"memory/notes.md":     "memory",
	})
	snippet := generateSourcesSnippet([]source.Source{src}, nil)

	for _, name := range []string{"deploy.md", "SKILL.md", "notes.md"} {
		if strings.Contains(snippet, name) {
			t.Errorf("managed dir file %q should not appear in sources snippet", name)
		}
	}
}

func TestGenerateSourcesSnippet_MixedAlwaysAndConditional(t *testing.T) {
	src := newFilledSource(t, "ai-rules", map[string]string{
		"CLAUDE.md":        "root",
		"dev/common.md":    "common",
		"dev/go/go.md":     "go",
		"dev/java/java.md": "java",
	})
	snippet := generateSourcesSnippet([]source.Source{src}, nil)

	if !strings.Contains(snippet, "Always read:") {
		t.Error("expected 'Always read:' section for always-load files")
	}
	if !strings.Contains(snippet, "also read:") {
		t.Error("expected 'also read:' section for conditional files")
	}
}

func TestGenerateSourcesSnippet_HasBeginAndEndMarkers(t *testing.T) {
	src := newFilledSource(t, "ai-rules", map[string]string{
		"CLAUDE.md": "root",
	})
	snippet := generateSourcesSnippet([]source.Source{src}, nil)

	if !strings.HasPrefix(snippet, sourcesBegin) {
		t.Error("snippet should start with sourcesBegin")
	}
	if !strings.HasSuffix(snippet, sourcesEnd) {
		t.Error("snippet should end with sourcesEnd")
	}
}

// ─── expandSourcesPlaceholder ─────────────────────────────────────────────────

func TestExpandSourcesPlaceholder_NoMarker(t *testing.T) {
	dir := t.TempDir()
	original := "# My Rules\nSome content with no sources markers.\n"
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), original)

	src := newFilledSource(t, "ai-rules", map[string]string{"CLAUDE.md": "root"})
	if err := expandSourcesPlaceholder(dir, []source.Source{src}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readFile(t, filepath.Join(dir, "CLAUDE.md"))
	if got != original {
		t.Error("file without marker should be unchanged")
	}
}

func TestExpandSourcesPlaceholder_NoClaudeMd(t *testing.T) {
	dir := t.TempDir() // no CLAUDE.md
	src := newFilledSource(t, "ai-rules", map[string]string{"CLAUDE.md": "root"})
	if err := expandSourcesPlaceholder(dir, []source.Source{src}, nil); err != nil {
		t.Fatalf("missing CLAUDE.md should not return error, got: %v", err)
	}
}

func TestExpandSourcesPlaceholder_RawPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "header\n"+sourcesPlaceholder+"\nfooter\n")

	src := newFilledSource(t, "ai-rules", map[string]string{"CLAUDE.md": "root"})
	if err := expandSourcesPlaceholder(dir, []source.Source{src}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readFile(t, filepath.Join(dir, "CLAUDE.md"))

	if strings.Contains(got, sourcesPlaceholder) {
		t.Error("raw placeholder should be replaced")
	}
	if !strings.Contains(got, sourcesBegin) {
		t.Error("expected begin marker in result")
	}
	if !strings.Contains(got, sourcesEnd) {
		t.Error("expected end marker in result")
	}
	if !strings.Contains(got, "header") || !strings.Contains(got, "footer") {
		t.Error("surrounding content should be preserved")
	}
}

func TestExpandSourcesPlaceholder_ExistingBlock(t *testing.T) {
	dir := t.TempDir()
	oldBlock := sourcesBegin + "\nold stale content\n" + sourcesEnd
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "before\n"+oldBlock+"\nafter\n")

	src := newFilledSource(t, "ai-rules", map[string]string{"CLAUDE.md": "root"})
	if err := expandSourcesPlaceholder(dir, []source.Source{src}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readFile(t, filepath.Join(dir, "CLAUDE.md"))

	if strings.Contains(got, "old stale content") {
		t.Error("old block content should be replaced")
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Error("surrounding content should be preserved")
	}
	if !strings.Contains(got, sourcesBegin) || !strings.Contains(got, sourcesEnd) {
		t.Error("expected refreshed begin/end block")
	}
}

func TestExpandSourcesPlaceholder_BothFormsPresent(t *testing.T) {
	dir := t.TempDir()
	oldBlock := sourcesBegin + "\nstale\n" + sourcesEnd
	content := "A\n" + sourcesPlaceholder + "\nB\n" + oldBlock + "\nC\n"
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), content)

	src := newFilledSource(t, "ai-rules", map[string]string{"CLAUDE.md": "root"})
	if err := expandSourcesPlaceholder(dir, []source.Source{src}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readFile(t, filepath.Join(dir, "CLAUDE.md"))

	if strings.Contains(got, sourcesPlaceholder) {
		t.Error("raw placeholder should have been replaced")
	}
	if strings.Contains(got, "stale") {
		t.Error("stale block content should have been replaced")
	}
	if !strings.Contains(got, "A") || !strings.Contains(got, "C") {
		t.Error("surrounding content should be preserved")
	}
}

func TestExpandSourcesPlaceholder_WithConditionalFiles(t *testing.T) {
	stagedDir := t.TempDir()
	writeFile(t, filepath.Join(stagedDir, "CLAUDE.md"), sourcesPlaceholder+"\n")

	src := newFilledSource(t, "ai-rules", map[string]string{
		"CLAUDE.md":    "root",
		"dev/go/go.md": "go rules",
	})
	if err := expandSourcesPlaceholder(stagedDir, []source.Source{src}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readFile(t, filepath.Join(stagedDir, "CLAUDE.md"))

	if !strings.Contains(got, knownStacks["go"].label) {
		t.Errorf("expected go condition in expanded result:\n%s", got)
	}
	if !strings.Contains(got, "go.md") {
		t.Errorf("expected go.md path in expanded result:\n%s", got)
	}
}

func TestExpandSourcesPlaceholder_WriteBackInstruction(t *testing.T) {
	stagedDir := t.TempDir()
	writeFile(t, filepath.Join(stagedDir, "CLAUDE.md"), sourcesPlaceholder+"\n")

	src := newFilledSource(t, "my-rules", map[string]string{"CLAUDE.md": "root"})
	p := &profile.Profile{WriteBack: profile.WriteBack{Default: "my-rules"}}
	if err := expandSourcesPlaceholder(stagedDir, []source.Source{src}, p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readFile(t, filepath.Join(stagedDir, "CLAUDE.md"))

	if !strings.Contains(got, "Primary source for edits:") {
		t.Errorf("expected write-back instruction in result:\n%s", got)
	}
}

// ─── resolvePrimarySource ─────────────────────────────────────────────────────

func TestResolvePrimarySource_ByName(t *testing.T) {
	srcs := []source.Source{
		{Name: "work", Root: "/work"},
		{Name: "personal", Root: "/personal"},
	}
	p := &profile.Profile{WriteBack: profile.WriteBack{Default: "personal"}}
	got := resolvePrimarySource(srcs, p)
	if !strings.Contains(got, "personal") {
		t.Errorf("expected /personal path, got %q", got)
	}
}

func TestResolvePrimarySource_FallbackToFirst(t *testing.T) {
	srcs := []source.Source{
		{Name: "first", Root: "/first"},
	}
	got := resolvePrimarySource(srcs, &profile.Profile{})
	if !strings.Contains(got, "first") {
		t.Errorf("expected fallback to first source path, got %q", got)
	}
}

func TestResolvePrimarySource_EmptySources(t *testing.T) {
	got := resolvePrimarySource(nil, nil)
	if got != "" {
		t.Errorf("expected empty string for nil sources, got %q", got)
	}
}
