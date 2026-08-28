package check_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/check"
	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// TestNiseOwnedFileLosingItsHeaderFails is the version-independent half of
// hand-edit detection. It has to keep working when the regeneration check
// cannot reach a verdict, so it is asserted here on its own rather than only
// through a whole-report pass.
func TestNiseOwnedFileLosingItsHeaderFails(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	body := readFile(t, root, "internal/app/modules.gen.go")
	writeFile(t, root, "internal/app/modules.gen.go",
		strings.Replace(body, "// "+generator.NiseOwnedHeader+"\n", "", 1))

	c := assertStatus(t, run(t, root), check.CheckOwnershipHeaders, check.StatusFail)
	if !containsPath(c.Paths, "internal/app/modules.gen.go") {
		t.Errorf("paths = %v, want it to name the file whose header was removed", c.Paths)
	}
}

// TestAppOwnedFileGainingTheNiseHeaderFails is the clobber direction: a file
// the application owns should never end up marked as generated output.
func TestAppOwnedFileGainingTheNiseHeaderFails(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	writeFile(t, root, "internal/app/app.go", "// "+generator.NiseOwnedHeader+"\n\npackage app\n")

	c := assertStatus(t, run(t, root), check.CheckOwnershipHeaders, check.StatusFail)
	if !containsPath(c.Paths, "internal/app/app.go") {
		t.Errorf("paths = %v, want it to name the clobbered application-owned file", c.Paths)
	}
}

// TestAppOwnedFileMayBeRewrittenFreely is the boundary this whole command is
// about. The application owns these files outright: deleting the courtesy
// header, replacing the contents, or removing the file altogether are all
// things BLUEPRINT §5 says nise does not police. A check that failed here
// would be enforcing starter conformity.
func TestAppOwnedFileMayBeRewrittenFreely(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	// Header gone, body replaced entirely.
	writeFile(t, root, "internal/app/app.go", "package app\n\n// entirely the application's own code now\n")
	// Another one deleted outright.
	if err := os.Remove(filepath.Join(root, "api", "README.md")); err != nil {
		t.Fatalf("removing an app-owned file: %v", err)
	}
	// And a directory the starter never created.
	writeFile(t, root, "internal/features/billing/service.go", "package billing\n")

	report := run(t, root)
	assertStatus(t, report, check.CheckOwnershipHeaders, check.StatusOK)
	if report.Failed() {
		for _, c := range report.Checks {
			if c.Status == check.StatusFail {
				t.Errorf("customizing application-owned files failed %q: %s", c.Name, c.Detail)
			}
		}
	}
}

// TestHandEditedSelfVerifyingFileFails covers the content-hash half of
// hand-edit detection, which stays conclusive regardless of which nise
// generated the project.
func TestHandEditedSelfVerifyingFileFails(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		path string
		edit func(string) string
	}{
		{
			name: "AGENTS.md body edited without recomputing its hash",
			path: "AGENTS.md",
			edit: func(s string) string { return s + "\nAn instruction somebody added by hand.\n" },
		},
		{
			name: "architecture.json value changed",
			path: ".nise/architecture.json",
			edit: func(s string) string {
				return strings.Replace(s, `"go-chi-postgres-svelte"`, `"something-else"`, 1)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := newProject(t)
			writeFile(t, root, tc.path, tc.edit(readFile(t, root, tc.path)))

			c := assertStatus(t, run(t, root), check.CheckOwnershipContent, check.StatusFail)
			if !containsPath(c.Paths, tc.path) {
				t.Errorf("paths = %v, want it to name %q", c.Paths, tc.path)
			}
			if !strings.Contains(c.Remedy, "nise agents --force") {
				t.Errorf("remedy = %q, want it to name the command that regenerates these files", c.Remedy)
			}
		})
	}
}

// TestOwnershipCheckNeverWritesToTheProject pins the no-side-effects rule.
// The content check reaches internal/agentsfile's verification through
// Write, aimed at a temporary copy; a mistake there would silently rewrite
// the very files it was asked to inspect.
func TestOwnershipCheckNeverWritesToTheProject(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	before := snapshot(t, root)

	// Break the hash so the verification takes its refusal path too.
	writeFile(t, root, "AGENTS.md", readFile(t, root, "AGENTS.md")+"\nedited\n")
	afterEdit := snapshot(t, root)

	run(t, root)

	if got := snapshot(t, root); got != afterEdit {
		t.Error("running the checks modified the project tree")
	}
	if before == afterEdit {
		t.Fatal("the test's own edit did not change the tree, so this test proves nothing")
	}
}

// TestNiseOwnedOutputIgnoresNameAndModulePath is what licenses this package
// to re-plan a project without knowing its real name or module path. If a
// future template makes a Nise-owned file depend on either, this test fails
// — and it must, because the regeneration check would otherwise start
// reporting phantom differences on any project whose directory was renamed.
func TestNiseOwnedOutputIgnoresNameAndModulePath(t *testing.T) {
	t.Parallel()

	base := generator.Options{
		Name:       "alpha",
		ModulePath: "github.com/example/alpha",
		Modules:    recipe.Modules(),
		CLIVersion: fixedVersion,
	}
	other := base
	other.Name = "beta"
	other.ModulePath = "example.org/totally/different/beta"

	first, err := generator.Plan(base)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	second, err := generator.Plan(other)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	firstOwned := niseOwned(first)
	secondOwned := niseOwned(second)
	if len(firstOwned) == 0 {
		t.Fatal("the plan contains no nise-owned files at all")
	}
	if len(firstOwned) != len(secondOwned) {
		t.Fatalf("the two plans own a different number of files: %d and %d", len(firstOwned), len(secondOwned))
	}
	for path, content := range firstOwned {
		if secondOwned[path] != content {
			t.Errorf("nise-owned %s depends on the application's name or module path", path)
		}
	}
}

func niseOwned(files []generator.File) map[string]string {
	out := make(map[string]string)
	for _, f := range files {
		if f.Owner == generator.OwnerNise {
			out[f.Path] = string(f.Content)
		}
	}
	return out
}

// snapshot renders a tree as a stable "path\0content" listing, for asserting
// that nothing changed.
//
// Paths are collected during the walk and read afterwards, so no filesystem
// operation happens inside the walk callback — the same shape
// internal/generator's own treeManifest uses, and what keeps gosec's G122
// (symlink TOCTOU in a walk callback) from having a real target here.
func snapshot(t *testing.T, root string) string {
	t.Helper()

	var paths []string
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	}); err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	slices.Sort(paths)

	var b strings.Builder
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatalf("relativizing %s: %v", p, err)
		}
		data, err := os.ReadFile(p) // #nosec G304 -- a path from walking a directory this test created.
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		b.WriteString(filepath.ToSlash(rel))
		b.WriteByte(0)
		b.Write(data)
		b.WriteByte(0)
	}
	return b.String()
}
