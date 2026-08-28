package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/check"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// TestRecipeFailures covers the ways a recipe stops being a recipe this
// build can act on, and — just as importantly — that each one produces a
// remedy that fits the actual failure. A recipe from a newer schema needs an
// upgrade, not a hand repair; telling that user to edit the file would be
// the wrong remedy.
func TestRecipeFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		content      string
		wantInRemedy string
	}{
		{
			name:         "not json at all",
			content:      "{ this is not json",
			wantInRemedy: "Restore",
		},
		{
			name:         "unknown schema version",
			content:      `{"schemaVersion":99,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v1.2.3","runtimeVersion":"v0.1.0"}`,
			wantInRemedy: "Upgrade nise",
		},
		{
			name:         "unknown profile",
			content:      `{"schemaVersion":1,"profile":"go-gin-mysql","modules":[],"cliVersion":"v1.2.3","runtimeVersion":"v0.1.0"}`,
			wantInRemedy: "Restore",
		},
		{
			name:         "unknown module",
			content:      `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":["telepathy"],"cliVersion":"v1.2.3","runtimeVersion":"v0.1.0"}`,
			wantInRemedy: "Restore",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := newProject(t)
			writeFile(t, root, recipe.FileName, tc.content)

			report := run(t, root)
			c := assertStatus(t, report, check.CheckRecipe, check.StatusFail)
			if !strings.Contains(c.Remedy, tc.wantInRemedy) {
				t.Errorf("remedy = %q, want it to contain %q", c.Remedy, tc.wantInRemedy)
			}
			if !report.Failed() {
				t.Error("report.Failed() = false with a broken recipe")
			}

			// Every check that needs a recipe must skip, not pile on
			// four more failures for one broken file.
			for _, dependent := range []string{
				check.CheckCLIVersion,
				check.CheckRuntimeVersion,
				check.CheckOwnershipHeaders,
				check.CheckOwnershipContent,
				check.CheckGeneration,
			} {
				assertStatus(t, report, dependent, check.StatusSkipped)
			}
		})
	}
}

// TestCLIVersionCompatibility covers the three outcomes of comparing the
// running nise against the recipe's, including the two that must skip rather
// than claim a verdict they did not reach.
func TestCLIVersionCompatibility(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		running string
		want    check.Status
	}{
		{"newer nise is fine", "v2.0.0", check.StatusOK},
		{"same nise is fine", fixedVersion, check.StatusOK},
		{"same version with build metadata is fine", fixedVersion + "+dirty", check.StatusOK},
		{"older nise is a compatibility failure", "v1.0.0", check.StatusFail},
		{"an unorderable running version is a skip", "dev", check.StatusSkipped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			report := run(t, newProject(t), func(o *check.Options) { o.CLIVersion = tc.running })
			assertStatus(t, report, check.CheckCLIVersion, tc.want)
		})
	}
}

// TestRuntimeVersionConsistency covers the recipe telling the truth about
// which runtime the project targets, and the several ways it cannot be
// compared — every one of which is a skip, because a project part-way
// through the documented detach path (BLUEPRINT §5) is not a broken project.
func TestRuntimeVersionConsistency(t *testing.T) {
	t.Parallel()

	t.Run("agreeing go.mod and recipe pass", func(t *testing.T) {
		t.Parallel()
		assertStatus(t, run(t, newProject(t)), check.CheckRuntimeVersion, check.StatusOK)
	})

	t.Run("a disagreeing go.mod fails and names both files", func(t *testing.T) {
		t.Parallel()

		root := newProject(t)
		goMod := readFile(t, root, "go.mod")
		writeFile(t, root, "go.mod", strings.Replace(goMod, "nise-and-go v0.1.0", "nise-and-go v9.9.9", 1))

		c := assertStatus(t, run(t, root), check.CheckRuntimeVersion, check.StatusFail)
		for _, want := range []string{"go.mod", recipe.FileName} {
			if !containsPath(c.Paths, want) {
				t.Errorf("paths = %v, want it to name %q", c.Paths, want)
			}
		}
	})

	t.Run("a project that no longer requires the runtime skips", func(t *testing.T) {
		t.Parallel()

		root := newProject(t)
		writeFile(t, root, "go.mod", "module github.com/example/myapp\n\ngo 1.26.0\n")

		assertStatus(t, run(t, root), check.CheckRuntimeVersion, check.StatusSkipped)
	})

	t.Run("a missing go.mod skips", func(t *testing.T) {
		t.Parallel()

		root := newProject(t)
		if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
			t.Fatalf("removing go.mod: %v", err)
		}

		assertStatus(t, run(t, root), check.CheckRuntimeVersion, check.StatusSkipped)
	})
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}
