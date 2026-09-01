package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// upgradeProject generates a project and ages its recipe, then runs the CLI
// from inside it. The working directory has to change because `nise upgrade`
// finds the project the way a developer expects — by walking up from where
// they are standing.
func upgradeProject(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app")
	if _, err := generator.Write(root, generator.Options{
		Name: "app", ModulePath: "example.com/app", CLIVersion: "v0.0.1",
	}); err != nil {
		t.Fatalf("generating a project: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, recipe.FileName)) // #nosec G304 -- a path under this test's own t.TempDir().
	if err != nil {
		t.Fatalf("reading the recipe: %v", err)
	}
	r, err := recipe.Unmarshal(data)
	if err != nil {
		t.Fatalf("parsing the recipe: %v", err)
	}
	r.CLIVersion = "v0.0.0-20250101000000-aaaaaaaaaaaa"
	r.RuntimeVersion = "v0.0.1"
	encoded, err := recipe.Marshal(r)
	if err != nil {
		t.Fatalf("encoding the recipe: %v", err)
	}
	// #nosec G703 -- root is under this test's own t.TempDir().
	if err := os.WriteFile(filepath.Join(root, recipe.FileName), encoded, 0o600); err != nil {
		t.Fatalf("writing the recipe: %v", err)
	}
	return root
}

// runIn executes the CLI with the process working directory set to dir.
// Chdir makes these cases serial, which is why none of them is parallel.
func runIn(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	before, err := os.Getwd()
	if err != nil {
		t.Fatalf("reading the working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("entering %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(before) })

	var stdout, stderr bytes.Buffer
	code := Execute(args, IO{Stdout: &stdout, Stderr: &stderr, Getenv: emptyGetenv})
	return code, stdout.String(), stderr.String()
}

// A dry run that modifies the tree is worse than no dry run at all.
func TestUpgradeDryRunWritesNothing(t *testing.T) {
	root := upgradeProject(t)
	original := mustRead(t, filepath.Join(root, "go.mod"))

	code, stdout, stderr := runIn(t, root, "upgrade", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code %d\nstdout=%q\nstderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Would upgrade") {
		t.Errorf("the dry run does not read as hypothetical: %q", stdout)
	}
	if !strings.Contains(stdout, "Nothing was written") {
		t.Errorf("the dry run does not say it wrote nothing: %q", stdout)
	}
	if mustRead(t, filepath.Join(root, "go.mod")) != original {
		t.Error("--dry-run modified go.mod")
	}
}

// The whole review mechanism is `git diff`, and a diff that also contains half
// a feature somebody was in the middle of is not one anybody reads.
func TestUpgradeRefusesADirtyWorkingTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed, and the dirty-tree guard is a git check")
	}
	root := upgradeProject(t)
	gitInit(t, root)
	// #nosec G703 -- root is under this test's own t.TempDir().
	if err := os.WriteFile(filepath.Join(root, "unfinished.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatalf("writing an uncommitted file: %v", err)
	}

	code, _, stderr := runIn(t, root, "upgrade")
	if code != 3 {
		t.Errorf("exit code %d, want 3 (precondition)", code)
	}
	if !strings.Contains(stderr, "uncommitted changes") {
		t.Errorf("stderr does not say why it refused: %q", stderr)
	}
	if !strings.Contains(stderr, "--dry-run") || !strings.Contains(stderr, "--force") {
		t.Errorf("stderr does not offer the two ways forward: %q", stderr)
	}
}

// A clean tree is not refused, and --dry-run is never refused: looking is
// always safe.
func TestUpgradeAllowsACleanTreeAndAlwaysAllowsLooking(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed, and the dirty-tree guard is a git check")
	}
	root := upgradeProject(t)
	gitInit(t, root)

	if code, _, stderr := runIn(t, root, "upgrade", "--dry-run"); code != 0 {
		t.Errorf("--dry-run on a clean tree exited %d: %s", code, stderr)
	}
	if code, _, stderr := runIn(t, root, "upgrade"); code != 0 {
		t.Errorf("upgrade on a clean tree exited %d: %s", code, stderr)
	}
}

// A project that is not a git repository is not refused. `nise new` never runs
// git, and requiring it would be inventing a prerequisite.
func TestUpgradeDoesNotRequireAGitRepository(t *testing.T) {
	root := upgradeProject(t)
	if code, _, stderr := runIn(t, root, "upgrade"); code != 0 {
		t.Errorf("upgrading a project that is not a git repository exited %d: %s", code, stderr)
	}
}

func TestUpgradeOutsideAProjectSaysSo(t *testing.T) {
	code, _, stderr := runIn(t, t.TempDir(), "upgrade")
	if code != 3 {
		t.Errorf("exit code %d, want 3 (precondition)", code)
	}
	if !strings.Contains(stderr, "no nise.json found") {
		t.Errorf("stderr does not name the missing recipe: %q", stderr)
	}
}

func TestUpgradeJSONShape(t *testing.T) {
	root := upgradeProject(t)

	code, stdout, stderr := runIn(t, root, "--json", "upgrade", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code %d\nstdout=%q\nstderr=%q", code, stdout, stderr)
	}
	var got upgradeResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &got); err != nil {
		t.Fatalf("the output is not one JSON document: %v\n%s", err, stdout)
	}
	if !got.DryRun {
		t.Error("the JSON does not record that this was a dry run, so a script cannot tell whether anything was written")
	}
	if got.RuntimeFrom != "v0.0.1" || got.RuntimeTo != generator.NiseModuleVersion {
		t.Errorf("runtime %s → %s, want v0.0.1 → %s", got.RuntimeFrom, got.RuntimeTo, generator.NiseModuleVersion)
	}
}

// The output's closing line is the claim the whole design rests on, and a
// person reads the last line.
func TestUpgradeSaysThatApplicationCodeWasNotRegenerated(t *testing.T) {
	root := upgradeProject(t)

	_, stdout, _ := runIn(t, root, "upgrade")
	if !strings.Contains(stdout, "No application-owned file was regenerated") {
		t.Errorf("the output does not state the property that makes an upgrade safe: %q", stdout)
	}
	if !strings.Contains(stdout, "Review the diff") {
		t.Errorf("the output does not send the reader to the diff: %q", stdout)
	}
}

// An older nise cannot know what a newer one wrote, and regenerating with it
// would quietly revert files.
func TestUpgradeRefusesADowngrade(t *testing.T) {
	t.Parallel()

	if err := refuseDowngrade("v0.1.0", "v0.2.0"); err == nil {
		t.Error("an older nise was allowed to upgrade a project a newer one wrote")
	}
	if err := refuseDowngrade("v0.2.0", "v0.1.0"); err != nil {
		t.Errorf("a newer nise was refused: %v", err)
	}
	if err := refuseDowngrade("v0.1.0", "v0.1.0"); err != nil {
		t.Errorf("the same nise was refused: %v", err)
	}
	// A build from a checkout reports something no parser can compare.
	// Refusing there would make the framework's own development loop unusable,
	// and the ownership rules protect the project either way.
	if err := refuseDowngrade("dev", "v0.1.0"); err != nil {
		t.Errorf("a source build was refused: %v", err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- a path under this test's own t.TempDir().
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"add", "-A"},
		{"commit", "-qm", "generated"},
	} {
		// #nosec G204 -- fixed arguments, and root is this test's own t.TempDir().
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
