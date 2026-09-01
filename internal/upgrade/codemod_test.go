package upgrade_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/upgrade"
)

// A codemod is the one thing in this package permitted to edit
// application-owned source, so every property that makes that defensible is
// pinned here: it is exact, it is declared, it is counted, it is reported, and
// it can be refused.

// appGoFile returns the path of an application-owned Go file in a generated
// project, and its content.
func appGoFile(t *testing.T) (path, content string) {
	t.Helper()
	planned, err := generator.Plan(generator.Options{
		Name: "app", ModulePath: "example.com/app", CLIVersion: generatingCLI,
	})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	for _, file := range planned {
		if file.Owner == generator.OwnerApp && strings.HasSuffix(file.Path, ".go") {
			return file.Path, string(file.Content)
		}
	}
	t.Fatal("the generated project has no application-owned Go file, so these tests prove nothing")
	return "", ""
}

func withCodemod(root string, mods ...upgrade.Codemod) upgrade.Options {
	opts := options(root)
	opts.Migrations = []upgrade.Migration{{
		From: "v0.0.1", To: generator.NiseModuleVersion,
		Summary:  "a runtime package was renamed",
		Codemods: mods,
	}}
	return opts
}

const renamedImport = `"github.com/drilonrecica/nise-and-go/runtime/session"`

func TestACodemodRewritesApplicationSourceAndSaysWhereAndHowOften(t *testing.T) {
	t.Parallel()
	root := newProject(t)
	path, _ := appGoFile(t)
	write(t, root, path, "package thing\n\nimport (\n\t"+renamedImport+"\n)\n\nvar _ = session.Token{} // "+renamedImport+"\n")
	stale(t, root)

	opts := withCodemod(root, upgrade.Codemod{
		Describe: "runtime/session moved to runtime/sessions",
		Old:      renamedImport,
		New:      `"github.com/drilonrecica/nise-and-go/runtime/sessions"`,
	})

	report, err := upgrade.Apply(opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var found bool
	for _, change := range report.Codemods {
		if change.Path == path {
			found = true
			if change.Occurrences != 2 {
				t.Errorf("occurrences = %d, want 2 — a codemod that under-reports is one whose diff nobody can predict", change.Occurrences)
			}
			if change.Describe == "" {
				t.Error("the codemod is reported without saying what it was for")
			}
		}
	}
	if !found {
		t.Fatalf("the codemod is not reported against %s: %+v", path, report.Codemods)
	}

	after := read(t, root, path)
	if strings.Contains(after, renamedImport) {
		t.Errorf("the old import survives:\n%s", after)
	}
	if strings.Count(after, "runtime/sessions") != 2 {
		t.Errorf("the rewrite did not apply twice:\n%s", after)
	}
}

// A codemod that matched nothing is not in the report. An entry there is a
// change to review, and a list of no-ops buries the ones that are not.
func TestACodemodThatMatchesNothingIsNotReported(t *testing.T) {
	t.Parallel()
	root := newProject(t)
	stale(t, root)

	report, err := upgrade.Plan(withCodemod(root, upgrade.Codemod{
		Describe: "something that does not appear anywhere",
		Old:      "github.com/example/absent/v3",
		New:      "github.com/example/present/v4",
	}))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(report.Codemods) != 0 {
		t.Errorf("a codemod that matched nothing was reported: %+v", report.Codemods)
	}
}

// Codemods can be refused. A developer who would rather make the change by
// hand gets the instruction instead of the edit — and, crucially, still gets
// told the change exists rather than silently getting neither.
func TestCodemodsCanBeDisabledAndBecomeNotes(t *testing.T) {
	t.Parallel()
	root := newProject(t)
	path, _ := appGoFile(t)
	write(t, root, path, "package thing\n\nimport "+renamedImport+"\n")
	stale(t, root)

	opts := withCodemod(root, upgrade.Codemod{
		Describe: "runtime/session moved to runtime/sessions",
		Old:      renamedImport,
		New:      `"github.com/drilonrecica/nise-and-go/runtime/sessions"`,
	})
	opts.Codemods = false

	report, err := upgrade.Apply(opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Codemods) != 0 {
		t.Errorf("codemods were reported while disabled: %+v", report.Codemods)
	}
	if !anyContains(report.Notes, "Codemods are disabled") {
		t.Errorf("the disabled codemod was not carried into the notes: %v", report.Notes)
	}
	if !anyContains(report.Notes, "runtime/session moved to runtime/sessions") {
		t.Errorf("the note does not say what the change was: %v", report.Notes)
	}
	if !strings.Contains(read(t, root, path), renamedImport) {
		t.Error("the file was rewritten even though codemods were disabled")
	}
}

// A codemod may only touch files the generator plans as application-owned.
// A directory walk would also reach vendored code, node_modules, and whatever
// else the developer has put in the tree.
func TestACodemodNeverTouchesAFileNiseDidNotGenerate(t *testing.T) {
	t.Parallel()
	root := newProject(t)
	const theirs = "internal/theirown/thing.go"
	write(t, root, theirs, "package theirown\n\nimport "+renamedImport+"\n")
	stale(t, root)

	if _, err := upgrade.Apply(withCodemod(root, upgrade.Codemod{
		Describe: "runtime/session moved to runtime/sessions",
		Old:      renamedImport,
		New:      `"github.com/drilonrecica/nise-and-go/runtime/sessions"`,
	})); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(read(t, root, theirs), renamedImport) {
		t.Errorf("a file nise never generated was rewritten by a codemod:\n%s", read(t, root, theirs))
	}
}

// A Nise-owned file is regenerated, not codemodded. Running both over one file
// would rewrite bytes that were just written by the generator — corrupting
// generated output — and a codemod count against a generated file tells a
// reviewer nothing.
//
// Checked in both halves: the plan must not report it, and the file on disk
// must not have changed. Asserting only on the report leaves the applying path
// free to lose its filter with nothing failing.
func TestACodemodDoesNotAlsoRunOverNiseOwnedFiles(t *testing.T) {
	t.Parallel()
	root := newProject(t)
	stale(t, root)

	mod := upgrade.Codemod{
		Describe: "the ownership header itself",
		Old:      generator.NiseOwnedHeader,
		New:      "Code generated by something else; DO NOT EDIT.",
	}

	report, err := upgrade.Plan(withCodemod(root, mod))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, change := range report.Codemods {
		t.Errorf("a codemod matched the Nise-owned %s; only application-owned source is rewritten", change.Path)
	}

	if _, err := upgrade.Apply(withCodemod(root, mod)); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	planned, err := generator.Plan(generator.Options{
		Name: "app", ModulePath: "example.com/app", CLIVersion: generatingCLI,
	})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	// The precise claim: after the upgrade, every Nise-owned Go file on disk
	// is byte-for-byte what the generator planned. A codemod that ran over one
	// would have left different bytes. (Not every Nise-owned file carries the
	// Nise header — checked-in output from a pinned external generator keeps
	// that tool's own marker — so the comparison is against the plan, not
	// against a header.)
	var checked int
	for _, file := range planned {
		if file.Owner != generator.OwnerNise || !strings.HasSuffix(file.Path, ".go") {
			continue
		}
		checked++
		if got := read(t, root, file.Path); got != string(file.Content) {
			t.Errorf("the Nise-owned %s on disk is not what the generator planned; a codemod rewrote generated output", file.Path)
		}
	}
	if checked == 0 {
		t.Fatal("the generated project has no Nise-owned Go files, so this test proves nothing")
	}
}

// A file the application deleted stays deleted. Deleting an app-owned file is
// the application's prerogative, and re-creating one to codemod it would be
// the upgrade overruling that.
func TestACodemodDoesNotResurrectADeletedFile(t *testing.T) {
	t.Parallel()
	root := newProject(t)
	path, _ := appGoFile(t)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(path))); err != nil {
		t.Fatalf("deleting %s: %v", path, err)
	}
	stale(t, root)

	if _, err := upgrade.Apply(withCodemod(root, upgrade.Codemod{
		Describe: "runtime/session moved to runtime/sessions",
		Old:      renamedImport,
		New:      `"github.com/drilonrecica/nise-and-go/runtime/sessions"`,
	})); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); !os.IsNotExist(err) {
		t.Errorf("%s was re-created by the upgrade", path)
	}
}

// A gap in the migration chain is reported rather than skipped. An upgrade
// that silently jumped from v0.1.0 to v0.4.0 because no v0.2.0 entry existed
// would leave a project half-migrated with nothing in the output to say so.
func TestAMissingMigrationLinkIsReported(t *testing.T) {
	t.Parallel()
	root := newProject(t)
	stale(t, root)

	opts := options(root)
	// The chain starts at v0.0.1 and this table starts at v0.0.2: nothing
	// connects them.
	opts.Migrations = []upgrade.Migration{{
		From: "v0.0.2", To: generator.NiseModuleVersion, Summary: "unreachable",
	}}

	report, err := upgrade.Plan(opts)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !anyContains(report.Notes, "No migration is declared from v0.0.1") {
		t.Errorf("the gap in the migration chain is not reported: %v", report.Notes)
	}
}

// The declared table this project actually ships. It is empty, and that is the
// honest state of a first release rather than an omission — but an entry that
// names a version pair going the wrong way, or a codemod with an empty Old,
// would be a table nobody noticed was broken until an upgrade ran it.
func TestTheDeclaredMigrationsAreWellFormed(t *testing.T) {
	t.Parallel()

	for _, migration := range upgrade.Declared() {
		if migration.From == "" || migration.To == "" {
			t.Errorf("a migration declares From=%q To=%q", migration.From, migration.To)
		}
		if migration.From == migration.To {
			t.Errorf("the migration from %s goes nowhere", migration.From)
		}
		if migration.Summary == "" {
			t.Errorf("the migration %s → %s has no summary, so an upgrade would apply it silently", migration.From, migration.To)
		}
		for _, mod := range migration.Codemods {
			if mod.Old == "" || mod.New == "" || mod.Describe == "" {
				t.Errorf("the migration %s → %s declares an incomplete codemod: %+v", migration.From, migration.To, mod)
			}
			if len(mod.Old) < 8 {
				t.Errorf("the codemod %q replaces %q, which is short enough that a coincidental match is plausible", mod.Describe, mod.Old)
			}
		}
	}
}
