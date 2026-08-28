package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/check"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// TestHandEditedNiseOwnedFileFailsRegeneration is the check's headline
// behavior: an edit to a file nise owns is a change that will be silently
// lost, and the run has to exit non-zero because of it.
func TestHandEditedNiseOwnedFileFailsRegeneration(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"internal/app/modules.gen.go",
		"internal/platform/webui/webui.go",
		"internal/platform/webui/embedded/placeholder.html",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			root := newProject(t)
			writeFile(t, root, path, readFile(t, root, path)+"\nedited by hand\n")

			report := run(t, root)
			c := assertStatus(t, report, check.CheckGeneration, check.StatusFail)
			if !containsPath(c.Paths, path) {
				t.Errorf("paths = %v, want it to name %q", c.Paths, path)
			}
			if !report.Failed() {
				t.Error("report.Failed() = false after a nise-owned file was edited")
			}
		})
	}
}

// TestReformattedRecipeFailsRegeneration covers the recipe's own
// canonicalization: nise.json carries no ownership header (JSON has no
// comment syntax), so regeneration is the only thing that notices it was
// rewritten into a form nise would not have written.
func TestReformattedRecipeFailsRegeneration(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	// Still valid, still the same values, but not the canonical encoding.
	writeFile(t, root, recipe.FileName,
		`{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"`+fixedVersion+`","runtimeVersion":"v0.1.0"}`)

	report := run(t, root)
	assertStatus(t, report, check.CheckRecipe, check.StatusOK)
	c := assertStatus(t, report, check.CheckGeneration, check.StatusFail)
	if !containsPath(c.Paths, recipe.FileName) {
		t.Errorf("paths = %v, want it to name %q", c.Paths, recipe.FileName)
	}
}

// TestDeletedNiseOwnedFileFailsRegeneration covers removal, not just
// modification. A missing nise-owned file is a project that no longer
// contains what its recipe says it does.
func TestDeletedNiseOwnedFileFailsRegeneration(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	if err := os.Remove(filepath.Join(root, "internal", "app", "modules.gen.go")); err != nil {
		t.Fatalf("removing a nise-owned file: %v", err)
	}

	c := assertStatus(t, run(t, root), check.CheckGeneration, check.StatusFail)
	if !containsPath(c.Paths, "internal/app/modules.gen.go") {
		t.Errorf("paths = %v, want it to name the deleted file", c.Paths)
	}
}

// TestADifferentNiseDowngradesRegenerationToASkip is the wrong-remedy guard,
// and the single most important assertion in this file. When the running
// nise is not the one that generated the project, a difference could be a
// hand edit or a template change between the two versions — and only one of
// those has "restore the file" as its remedy. `nise doctor` shipped a check
// that guessed in this exact situation and printed advice that was actively
// wrong; this one reports what it saw and says why it cannot conclude.
func TestADifferentNiseDowngradesRegenerationToASkip(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	writeFile(t, root, "internal/app/modules.gen.go", readFile(t, root, "internal/app/modules.gen.go")+"\nedited\n")

	report := run(t, root, func(o *check.Options) { o.CLIVersion = "v9.9.9" })

	c := assertStatus(t, report, check.CheckGeneration, check.StatusSkipped)
	if report.Failed() {
		t.Error("a version mismatch must not fail the run through the regeneration check")
	}
	// The finding is still reported — a skip is not silence.
	if !containsPath(c.Paths, "internal/app/modules.gen.go") {
		t.Errorf("paths = %v, want the differing file reported even though the check skipped", c.Paths)
	}
	if !strings.Contains(c.Detail, "v9.9.9") || !strings.Contains(c.Detail, fixedVersion) {
		t.Errorf("detail = %q, want it to name both versions", c.Detail)
	}
	// And the ownership checks must still be covering hand edits, which is
	// what makes the skip safe rather than a hole.
	assertStatus(t, report, check.CheckOwnershipHeaders, check.StatusOK)
	assertStatus(t, report, check.CheckOwnershipContent, check.StatusOK)
}

// TestBuildMetadataIsNotAVersionDifference is the regression test for the
// skip above becoming too eager. A binary built from a dirty source checkout
// reports the recipe's own pseudo-version with "+dirty" appended: the same
// templates, a different string. Comparing strings there would silently turn
// every hand-edit check in a framework developer's own test project into a
// skip with exit 0.
func TestBuildMetadataIsNotAVersionDifference(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	writeFile(t, root, "internal/app/modules.gen.go", readFile(t, root, "internal/app/modules.gen.go")+"\nedited\n")

	report := run(t, root, func(o *check.Options) { o.CLIVersion = fixedVersion + "+dirty" })

	assertStatus(t, report, check.CheckGeneration, check.StatusFail)
	if !report.Failed() {
		t.Error("a hand edit must fail even when the running build carries build metadata")
	}
}

// TestModuleSelectionIsRegeneratedFromTheRecipe checks that the regeneration
// reads the recipe rather than assuming the default selection: a project
// created with modules must regenerate with those modules, or every
// module-dependent generated file would report a phantom difference.
func TestModuleSelectionIsRegeneratedFromTheRecipe(t *testing.T) {
	t.Parallel()

	report := run(t, newProject(t, recipe.Modules()...))
	assertStatus(t, report, check.CheckGeneration, check.StatusOK)
}

// TestRenamedProjectDirectoryStillRegeneratesCleanly covers the fallback in
// plan.go from the outside: the project's real name is recovered from its
// directory, and a directory renamed to something `nise new` would not have
// accepted must not turn into a wall of phantom differences.
func TestRenamedProjectDirectoryStillRegeneratesCleanly(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	renamed := filepath.Join(filepath.Dir(root), "Not A Valid Name")
	if err := os.Rename(root, renamed); err != nil {
		t.Fatalf("renaming the project directory: %v", err)
	}

	report := run(t, renamed)
	assertStatus(t, report, check.CheckGeneration, check.StatusOK)
	assertStatus(t, report, check.CheckOwnershipHeaders, check.StatusOK)
}
