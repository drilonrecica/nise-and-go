package check_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/check"
	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// fixedVersion is the CLI version every fixture project is generated with,
// so that the version-sensitive checks are exercised deterministically
// rather than against whatever the test binary's own build reports.
const fixedVersion = "v1.2.3"

// newProject generates a real project into a temporary directory and returns
// its root. Every test in this file runs against genuinely generated output
// rather than a hand-built fixture: a fixture would be a second, silently
// diverging copy of what `nise new` writes, and the checks under test are
// precisely the ones that notice when two copies disagree.
func newProject(t *testing.T, modules ...recipe.Module) string {
	t.Helper()

	root := filepath.Join(t.TempDir(), "myapp")
	if _, err := generator.Write(root, generator.Options{
		Name:       "myapp",
		ModulePath: "github.com/example/myapp",
		Modules:    modules,
		CLIVersion: fixedVersion,
	}); err != nil {
		t.Fatalf("generating the fixture project: %v", err)
	}
	return root
}

// noGit is a TrackedLister that reports no git repository, which is the
// state of a freshly generated project. Tests that care about the tracked-
// file path supply their own.
func noGit(_ context.Context, _ string) ([]string, bool, error) { return nil, false, nil }

// run evaluates every check against root with the fixture version.
func run(t *testing.T, root string, opts ...func(*check.Options)) check.Report {
	t.Helper()

	o := check.Options{Root: root, CLIVersion: fixedVersion, Git: noGit}
	for _, fn := range opts {
		fn(&o)
	}
	return check.Run(context.Background(), o)
}

// byName returns the named check, failing the test when the report does not
// contain one.
func byName(t *testing.T, report check.Report, name string) check.Check {
	t.Helper()

	for _, c := range report.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("report has no check named %q", name)
	return check.Check{}
}

// assertStatus requires a named check to have an exact status, printing the
// check's own detail on failure — the detail is what says why, and a bare
// "want ok, got fail" would send the reader back to the source.
func assertStatus(t *testing.T, report check.Report, name string, want check.Status) check.Check {
	t.Helper()

	c := byName(t, report, name)
	if c.Status != want {
		t.Errorf("check %q: status = %q, want %q\n  detail: %s\n  remedy: %s", name, c.Status, want, c.Detail, c.Remedy)
	}
	return c
}

// TestFreshProjectPassesEveryApplicableCheck is the first-context test, and
// the one `nise doctor`'s withdrawn tool-directive check would have failed:
// the very first thing a user does after `nise new` must not report a
// failure. Everything that can be verified is verified, and everything else
// says why it was not.
func TestFreshProjectPassesEveryApplicableCheck(t *testing.T) {
	t.Parallel()

	report := run(t, newProject(t))

	if report.Failed() {
		for _, c := range report.Checks {
			if c.Status == check.StatusFail {
				t.Errorf("freshly generated project fails %q: %s", c.Name, c.Detail)
			}
		}
	}

	for _, name := range []string{
		check.CheckRecipe,
		check.CheckCLIVersion,
		check.CheckRuntimeVersion,
		check.CheckOwnershipHeaders,
		check.CheckOwnershipContent,
		check.CheckGeneration,
		check.CheckSecrets,
	} {
		assertStatus(t, report, name, check.StatusOK)
	}

	// The two that cannot be evaluated must say so rather than pass.
	assertStatus(t, report, check.CheckMigrations, check.StatusSkipped)
	assertStatus(t, report, check.CheckConfig, check.StatusSkipped)
}

// TestEveryCheckStatesItsInvariantAndDetail enforces the honest-status rule
// structurally: a check that reports a status without saying what it
// protects and what it saw is unreadable, and a skipped check with no reason
// is indistinguishable from a pass.
func TestEveryCheckStatesItsInvariantAndDetail(t *testing.T) {
	t.Parallel()

	for _, c := range run(t, newProject(t)).Checks {
		if strings.TrimSpace(c.Invariant) == "" {
			t.Errorf("check %q states no invariant", c.Name)
		}
		if strings.TrimSpace(c.Detail) == "" {
			t.Errorf("check %q reports status %q with no detail", c.Name, c.Status)
		}
		if c.Status == check.StatusFail && strings.TrimSpace(c.Remedy) == "" {
			t.Errorf("check %q failed with no remedy", c.Name)
		}
	}
}

// TestReportOrderIsStable pins the evaluation order. Scripts read the JSON
// stream in order, and Global Constraint 5 requires identical output across
// runs.
func TestReportOrderIsStable(t *testing.T) {
	t.Parallel()

	want := []string{
		check.CheckRecipe,
		check.CheckCLIVersion,
		check.CheckRuntimeVersion,
		check.CheckOwnershipHeaders,
		check.CheckOwnershipContent,
		check.CheckGeneration,
		check.CheckMigrations,
		check.CheckConfig,
		check.CheckSecrets,
	}

	report := run(t, newProject(t))
	if len(report.Checks) != len(want) {
		t.Fatalf("report has %d checks, want %d", len(report.Checks), len(want))
	}
	for i, name := range want {
		if report.Checks[i].Name != name {
			t.Errorf("check %d is %q, want %q", i, report.Checks[i].Name, name)
		}
	}
}

// TestFindRootWalksUp covers the project-root convention: a command run from
// a subdirectory of a project is still run against the project.
func TestFindRootWalksUp(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	deep := filepath.Join(root, "internal", "platform", "config")

	got, found, err := check.FindRoot(deep)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	if !found {
		t.Fatal("FindRoot did not find the project root from a subdirectory")
	}
	if got != root {
		t.Errorf("FindRoot = %q, want %q", got, root)
	}
}

// TestFindRootStopsAtTheFilesystemRoot covers the other direction: outside
// any project the walk terminates rather than looping, and reports not found
// rather than an error.
func TestFindRootStopsAtTheFilesystemRoot(t *testing.T) {
	t.Parallel()

	_, found, err := check.FindRoot(t.TempDir())
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	if found {
		t.Error("FindRoot reported a project root in an empty temporary directory")
	}
}

// writeFile overwrites a file inside a fixture project.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating the directory for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
}

// readFile reads a file inside a fixture project.
func readFile(t *testing.T, root, rel string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(data)
}
