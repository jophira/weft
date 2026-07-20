package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jophira/weft/internal/manifest"
	"github.com/jophira/weft/internal/profile"
	"github.com/jophira/weft/internal/source"
)

// adoptFixture wires an isolated config with a claude-code target root, a
// manifest pointing at it, and the named sources registered against the active
// profile. It returns the target root and the source roots by name.
func adoptFixture(t *testing.T, sourceNames ...string) (base, targetRoot string, srcRoots map[string]string) {
	t.Helper()
	base = withIsolatedConfig(t)
	targetRoot = filepath.Join(base, "claude")
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := manifest.Save(base, &manifest.Manifest{
		Harness: "claude-code", Profile: "test", TargetRoot: targetRoot,
		Files: map[string]string{},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	reg, err := newRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	srcRoots = map[string]string{}
	for _, name := range sourceNames {
		root := filepath.Join(base, "sources", name)
		if mkErr := os.MkdirAll(root, 0o755); mkErr != nil {
			t.Fatalf("mkdir source: %v", mkErr)
		}
		if addErr := reg.Add(source.Source{Name: name, Root: root, Structure: source.DefaultStructure()}); addErr != nil {
			t.Fatalf("add source %q: %v", name, addErr)
		}
		srcRoots[name] = root
	}
	pm, err := newProfileManager()
	if err != nil {
		t.Fatalf("profile manager: %v", err)
	}
	if err := pm.Create(profile.Profile{
		Name: "test", Sources: sourceNames, Overlay: profile.OverlayCascade,
	}); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	return base, targetRoot, srcRoots
}

// resetAdoptFlags restores the package-level flag vars, which cobra would
// otherwise leak between tests.
func resetAdoptFlags(t *testing.T) {
	t.Helper()
	prevScan, prevInto, prevForce, prevYes := adoptScan, adoptInto, adoptForce, adoptYes
	t.Cleanup(func() { adoptScan, adoptInto, adoptForce, adoptYes = prevScan, prevInto, prevForce, prevYes })
	adoptScan, adoptInto, adoptForce, adoptYes = false, "", false, false
}

// runAdoptCmd invokes adoptCmd's RunE and returns its captured output and error.
func runAdoptCmd(args []string) (string, error) {
	buf := &bytes.Buffer{}
	holder := &cobra.Command{}
	holder.SetOut(buf)
	err := adoptCmd.RunE(holder, args)
	return buf.String(), err
}

func TestAdoptScan_listsUnownedIgnoresOwned(t *testing.T) {
	resetAdoptFlags(t)
	base, targetRoot, _ := adoptFixture(t, "personal")
	writeFileT(t, filepath.Join(targetRoot, "agents", "reviewer.md"), "# reviewer\n")
	writeFileT(t, filepath.Join(targetRoot, "commands", "owned.md"), "# owned\n")
	writeFileT(t, filepath.Join(targetRoot, "CLAUDE.md"), "# instructions\n")

	ownedRel := filepath.Join("commands", "owned.md")
	if err := manifest.Save(base, &manifest.Manifest{
		Harness: "claude-code", TargetRoot: targetRoot,
		Files: map[string]string{ownedRel: manifest.HashBytes([]byte("# owned\n"))},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	adoptScan = true
	out, err := runAdoptCmd(nil)
	if err != nil {
		t.Fatalf("adopt --scan: %v", err)
	}
	if !strings.Contains(out, filepath.Join("agents", "reviewer.md")) {
		t.Errorf("scan missed the unowned agent:\n%s", out)
	}
	for _, unwanted := range []string{"owned.md", "CLAUDE.md"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("scan surfaced %q, which is not adoptable:\n%s", unwanted, out)
		}
	}
	// A single source means the suggested command can be concrete.
	if !strings.Contains(out, "--into personal") {
		t.Errorf("scan did not suggest the only source:\n%s", out)
	}
}

func TestAdoptScan_reportsNothingToAdopt(t *testing.T) {
	resetAdoptFlags(t)
	adoptFixture(t, "personal")
	adoptScan = true
	out, err := runAdoptCmd(nil)
	if err != nil {
		t.Fatalf("adopt --scan: %v", err)
	}
	if !strings.Contains(out, "No adoptable files") {
		t.Errorf("empty scan output = %q", out)
	}
}

func TestAdopt_copiesToClassCorrectPath(t *testing.T) {
	resetAdoptFlags(t)
	_, targetRoot, srcRoots := adoptFixture(t, "personal")
	rel := filepath.Join("agents", "reviewer.md")
	writeFileT(t, filepath.Join(targetRoot, rel), "# reviewer\n")

	adoptInto, adoptYes = "personal", true
	out, err := runAdoptCmd([]string{"claude-code", rel})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	dst := filepath.Join(srcRoots["personal"], "agents", "reviewer.md")
	got, readErr := os.ReadFile(dst)
	if readErr != nil || string(got) != "# reviewer\n" {
		t.Fatalf("expected adopted file at %s; got %q err %v\noutput:\n%s", dst, got, readErr, out)
	}
	if !strings.Contains(out, "adopted 1 file(s)") {
		t.Errorf("adopt output = %q", out)
	}
}

func TestAdopt_ambiguousIntoErrors(t *testing.T) {
	resetAdoptFlags(t)
	_, targetRoot, _ := adoptFixture(t, "personal", "work")
	rel := filepath.Join("agents", "reviewer.md")
	writeFileT(t, filepath.Join(targetRoot, rel), "# reviewer\n")

	adoptYes = true
	_, err := runAdoptCmd([]string{"claude-code", rel})
	if err == nil {
		t.Fatal("expected an error when --into is omitted with several sources")
	}
	for _, want := range []string{"--into is required", "personal", "work"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestAdopt_refusesExistingDestinationWithoutForce(t *testing.T) {
	resetAdoptFlags(t)
	_, targetRoot, srcRoots := adoptFixture(t, "personal")
	rel := filepath.Join("agents", "reviewer.md")
	writeFileT(t, filepath.Join(targetRoot, rel), "# harness copy\n")
	dst := filepath.Join(srcRoots["personal"], "agents", "reviewer.md")
	writeFileT(t, dst, "# source copy\n")

	adoptInto, adoptYes = "personal", true
	_, err := runAdoptCmd([]string{"claude-code", rel})
	if err == nil {
		t.Fatal("expected a refusal when the destination already exists")
	}
	if !strings.Contains(err.Error(), "--force") || !strings.Contains(err.Error(), "reviewer.md") {
		t.Errorf("error %q should name the conflicting file and --force", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "# source copy\n" {
		t.Errorf("source copy was clobbered: %q", got)
	}

	adoptForce = true
	if _, err := runAdoptCmd([]string{"claude-code", rel}); err != nil {
		t.Fatalf("adopt --force: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "# harness copy\n" {
		t.Errorf("--force did not overwrite: %q", got)
	}
}

func TestAdopt_refusesSecretBearingFile(t *testing.T) {
	resetAdoptFlags(t)
	_, targetRoot, srcRoots := adoptFixture(t, "personal")
	rel := filepath.Join("agents", "leaky.md")
	writeFileT(t, filepath.Join(targetRoot, rel), "Use ANTHROPIC_API_KEY=sk-ant-api03-abcdef0123456789\n")

	adoptInto, adoptYes = "personal", true
	_, err := runAdoptCmd([]string{"claude-code", rel})
	if err == nil {
		t.Fatal("expected a refusal for a file carrying a literal credential")
	}
	if !strings.Contains(err.Error(), "credential") {
		t.Errorf("error %q should explain the credential guard", err)
	}
	if _, statErr := os.Stat(filepath.Join(srcRoots["personal"], "agents", "leaky.md")); statErr == nil {
		t.Error("secret-bearing file reached the source")
	}
}

func TestAdopt_requiresConfirmationWithoutYes(t *testing.T) {
	resetAdoptFlags(t)
	_, targetRoot, srcRoots := adoptFixture(t, "personal")
	rel := filepath.Join("agents", "reviewer.md")
	writeFileT(t, filepath.Join(targetRoot, rel), "# reviewer\n")
	// confirm() reads stdin; an empty stdin is a decline, which is the safe default.
	withEmptyStdin(t)

	adoptInto = "personal"
	out, err := runAdoptCmd([]string{"claude-code", rel})
	if err != nil {
		t.Fatalf("adopt without --yes: %v", err)
	}
	if !strings.Contains(out, "Aborted.") {
		t.Errorf("declining the prompt did not abort:\n%s", out)
	}
	if !strings.Contains(out, "weft overwrites these files") {
		t.Errorf("plan preview did not warn about the one-way door:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(srcRoots["personal"], "agents", "reviewer.md")); statErr == nil {
		t.Error("declined adoption still wrote to the source")
	}
}

// withEmptyStdin points os.Stdin at an empty file for the duration of the test.
func withEmptyStdin(t *testing.T) {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	prev := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = prev
		_ = f.Close()
	})
}
