package cmd

import (
	"strings"
	"testing"

	"github.com/jophira/weft/internal/source"
)

// auditDefault runs auditSourceRules with the default structure's managed dirs
// and project names — the common case for a registered source.
func auditDefault(t *testing.T, root string) ruleAudit {
	t.Helper()
	ds := source.DefaultStructure()
	a, err := auditSourceRules(root, ds.ManagedDirs(), buildNameSet(ds.EffectiveProjectDirNames()), ds.InstructionGlob)
	if err != nil {
		t.Fatalf("auditSourceRules: %v", err)
	}
	return a
}

const labeledCommon = "---\nlabel: common\ndetect: \"true\"\npriority: 0\n---\n\n# Common\n"

func TestSuggestRuleFrontMatter_KnownStackAtRoot(t *testing.T) {
	// work-tech-style layout: stack dir at the source root, no dev/ prefix.
	sug, kind := suggestRuleFrontMatter("java/springboot.md")
	if kind != "stack" {
		t.Fatalf("kind = %q, want stack", kind)
	}
	if !sug.Confident {
		t.Error("known stack suggestion should be confident")
	}
	if !strings.Contains(sug.Detect, "'pom.xml' in files") || !strings.Contains(sug.Detect, "'build.gradle' in files") {
		t.Errorf("detect = %q, want pom.xml/build.gradle predicate", sug.Detect)
	}
	if sug.Label != "springboot" {
		t.Errorf("label = %q, want springboot (filename stem)", sug.Label)
	}
}

func TestSuggestRuleFrontMatter_KnownStackUnderDev(t *testing.T) {
	// pers-tech-style layout: dev/<stack>/.
	sug, kind := suggestRuleFrontMatter("dev/go/go.md")
	if kind != "stack" || !strings.Contains(sug.Detect, "'go.mod' in files") {
		t.Errorf("kind=%q detect=%q, want stack + go.mod predicate", kind, sug.Detect)
	}
}

func TestSuggestRuleFrontMatter_CommonAndDoc(t *testing.T) {
	if sug, kind := suggestRuleFrontMatter("common/general-guidelines.md"); kind != "common" || sug.Detect != "true" {
		t.Errorf("common: kind=%q detect=%q, want common + true", kind, sug.Detect)
	}
	if sug, kind := suggestRuleFrontMatter("dev/doc/doc.md"); kind != "doc" || sug.Detect != "true" {
		t.Errorf("doc: kind=%q detect=%q, want doc + true", kind, sug.Detect)
	}
}

func TestSuggestRuleFrontMatter_Unknown(t *testing.T) {
	sug, kind := suggestRuleFrontMatter("notes/scratch.md")
	if kind != "unknown" {
		t.Fatalf("kind = %q, want unknown", kind)
	}
	if sug.Confident {
		t.Error("unknown-location suggestion must not be confident")
	}
}

func TestDetectPredicate(t *testing.T) {
	if got := detectPredicate([]string{"go.mod"}); got != "'go.mod' in files" {
		t.Errorf("single = %q", got)
	}
	if got := detectPredicate([]string{"pom.xml", "build.gradle"}); got != "'pom.xml' in files || 'build.gradle' in files" {
		t.Errorf("multi = %q", got)
	}
}

func TestAuditSourceRules_FlagsMissingInStackDir(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"common/general-guidelines.md": labeledCommon,
		"java/springboot.md":           "# Spring Boot\n\nno front-matter here\n",
	})
	a := auditDefault(t, root)
	if a.Labeled != 1 {
		t.Errorf("Labeled = %d, want 1", a.Labeled)
	}
	if len(a.Missing) != 1 || a.Missing[0].File != "java/springboot.md" {
		t.Fatalf("Missing = %+v, want just java/springboot.md", a.Missing)
	}
	if a.Candidates != 2 {
		t.Errorf("Candidates = %d, want 2 (1 labeled + 1 missing)", a.Candidates)
	}
	if !a.Missing[0].Suggest.Confident {
		t.Error("stack-dir suggestion should be confident")
	}
}

func TestAuditSourceRules_DoesNotFlagDocsOrReadme(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"common/general-guidelines.md": labeledCommon,
		"docs/ARCHITECTURE.md":         "# Architecture\n\nprose, not a rule\n",
		"docs/README.md":               "# Readme\n",
	})
	a := auditDefault(t, root)
	if len(a.Missing) != 0 {
		t.Errorf("Missing = %+v, want none (docs/ and README are not rules)", a.Missing)
	}
	if a.Candidates != 1 {
		t.Errorf("Candidates = %d, want 1 (only the labeled common)", a.Candidates)
	}
}

func TestAuditSourceRules_FlagsUnknownWithLabeledSibling(t *testing.T) {
	// A stray dir gains rule status once one of its files is labeled — a sibling
	// without front-matter is then very likely a forgotten annotation.
	root := buildSourceTree(t, map[string]string{
		"misc/first.md":  "---\nlabel: misc-first\ndetect: \"true\"\n---\n\n# First\n",
		"misc/second.md": "# Second\n\nforgot the header\n",
	})
	a := auditDefault(t, root)
	if len(a.Missing) != 1 || a.Missing[0].File != "misc/second.md" {
		t.Fatalf("Missing = %+v, want misc/second.md via sibling heuristic", a.Missing)
	}
	if a.Missing[0].Suggest.Confident {
		t.Error("sibling-only match should be flagged as review (not confident)")
	}
}

func TestAuditSourceRules_DuplicateLabel(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"a/one.md": "---\nlabel: dupe\ndetect: \"true\"\n---\n\n# One\n",
		"a/two.md": "---\nlabel: dupe\ndetect: \"true\"\n---\n\n# Two\n",
	})
	a := auditDefault(t, root)
	if len(a.Duplicates) != 1 {
		t.Fatalf("Duplicates = %+v, want 1", a.Duplicates)
	}
	// a/one.md sorts first, so a/two.md is the ignored duplicate.
	if a.Duplicates[0].File != "a/two.md" || !strings.Contains(a.Duplicates[0].Detail, "a/one.md") {
		t.Errorf("duplicate finding = %+v, want two.md ignored in favour of one.md", a.Duplicates[0])
	}
}

func TestAuditSourceRules_DanglingExtends(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"java/java.md": "---\nlabel: java\ndetect: \"'pom.xml' in files\"\nextends: [common-backend]\n---\n\n# Java\n",
	})
	a := auditDefault(t, root)
	if len(a.Dangling) != 1 {
		t.Fatalf("Dangling = %+v, want 1", a.Dangling)
	}
	if !strings.Contains(a.Dangling[0].Detail, "common-backend") {
		t.Errorf("dangling detail = %q, want mention of common-backend", a.Dangling[0].Detail)
	}
}

func TestSuggestRuleFrontMatter_DevCommonByStem(t *testing.T) {
	// pers-tech layout: common* is a filename stem directly under dev/.
	if sug, kind := suggestRuleFrontMatter("dev/common-backend.md"); kind != "common" || sug.Detect != "true" {
		t.Errorf("dev/common-backend.md: kind=%q detect=%q, want common + true", kind, sug.Detect)
	}
}

func TestSuggestRuleFrontMatter_TicketPathIsNotAStackRule(t *testing.T) {
	// A ticket nested under a stack dir must NOT be treated as a stack rule just
	// because the path contains a "java" segment.
	sug, kind := suggestRuleFrontMatter("java/tickets/DIGI-3523/DIGI-3523.md")
	if kind != "unknown" {
		t.Fatalf("kind = %q, want unknown (immediate parent is the ticket dir)", kind)
	}
	if sug.Confident {
		t.Error("a ticket path must not yield a confident suggestion")
	}
}

func TestAuditSourceRules_ExcludesTicketsAndInstructionWrapper(t *testing.T) {
	// Mirrors the real work-tech shape: stack rules + a CLAUDE.md wrapper + a
	// sensitive ticket tree. Only the genuine rule should surface.
	root := buildSourceTree(t, map[string]string{
		"java/springboot.md":                      "---\nlabel: java\ndetect: \"'pom.xml' in files\"\n---\n\n# Spring\n",
		"java/CLAUDE.md":                          "# Java review wrapper\nFiles are included below\n",
		"java/tickets/DIGI-3523/DIGI-3523.md":     "---\nproject: instrument-service\n---\n\n# Ticket\n",
		"java/tickets/DIGI-3523/DIGI-3523_est.md": "# Estimate\n",
	})
	a := auditDefault(t, root)
	if len(a.Missing) != 0 {
		t.Errorf("Missing = %+v, want none (CLAUDE.md wrapper and tickets excluded)", a.Missing)
	}
	if a.Labeled != 1 || a.Candidates != 1 {
		t.Errorf("Labeled=%d Candidates=%d, want 1/1 (only java/springboot.md)", a.Labeled, a.Candidates)
	}
}

func TestAuditSourceRules_HealthyTreeIsQuiet(t *testing.T) {
	root := buildSourceTree(t, map[string]string{
		"common/general-guidelines.md": labeledCommon,
		"java/java.md":                 "---\nlabel: java\ndetect: \"'pom.xml' in files\"\nextends: [common]\n---\n\n# Java\n",
	})
	a := auditDefault(t, root)
	if len(a.Missing) != 0 || len(a.Duplicates) != 0 || len(a.Dangling) != 0 {
		t.Errorf("healthy tree produced findings: %+v", a)
	}
	if a.Labeled != 2 || a.Candidates != 2 {
		t.Errorf("Labeled=%d Candidates=%d, want 2/2", a.Labeled, a.Candidates)
	}
}
