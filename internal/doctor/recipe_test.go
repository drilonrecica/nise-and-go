package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

func writeRecipeFile(t *testing.T, dir string, data []byte) {
	t.Helper()
	path := filepath.Join(dir, recipe.FileName)
	// #nosec G306 -- test fixture, not a production artifact; see
	// .golangci.yml's own scoped exclusion for this exact pattern.
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func TestCheckRecipeNotInAProject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	got, r := checkRecipe(dir)
	if got.Status != StatusSkipped {
		t.Fatalf("Status = %v, want %v", got.Status, StatusSkipped)
	}
	if r != nil {
		t.Error("expected a nil recipe when none was found")
	}
}

func TestCheckRecipeFoundInParentDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	data, err := recipe.Marshal(recipe.Recipe{
		SchemaVersion:  recipe.SchemaVersion,
		Profile:        recipe.ProfileGoChiPostgresSvelte,
		Modules:        nil,
		CLIVersion:     "v0.1.0",
		RuntimeVersion: "v0.1.0",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	writeRecipeFile(t, root, data)

	got, r := checkRecipe(nested)
	if got.Status != StatusOK {
		t.Fatalf("Status = %v, want %v (Found=%q)", got.Status, StatusOK, got.Found)
	}
	if r == nil {
		t.Fatal("expected a non-nil parsed recipe")
	}
	if r.CLIVersion != "v0.1.0" {
		t.Errorf("CLIVersion = %q, want %q", r.CLIVersion, "v0.1.0")
	}
}

func TestCheckRecipeMalformedJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRecipeFile(t, dir, []byte("{not json"))

	got, r := checkRecipe(dir)
	if got.Status != StatusFail {
		t.Fatalf("Status = %v, want %v", got.Status, StatusFail)
	}
	if r != nil {
		t.Error("expected a nil recipe for a file that failed to parse")
	}
	if got.Remedy == "" {
		t.Error("Remedy is empty for a failing check")
	}
}

func TestCheckRecipeNewerSchemaVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRecipeFile(t, dir, []byte(`{
		"schemaVersion": 99,
		"profile": "go-chi-postgres-svelte",
		"modules": [],
		"cliVersion": "v9.9.9",
		"runtimeVersion": "v9.9.9"
	}`))

	got, r := checkRecipe(dir)
	if got.Status != StatusFail {
		t.Fatalf("Status = %v, want %v", got.Status, StatusFail)
	}
	if r != nil {
		t.Error("expected a nil recipe for a schema version this build does not understand")
	}
	if got.Remedy == "" || !strings.Contains(got.Remedy, "Upgrade nise") {
		t.Errorf("Remedy = %q, want it to say to upgrade nise", got.Remedy)
	}
}

func TestCheckRecipeCompatibility(t *testing.T) {
	t.Parallel()

	t.Run("nil recipe skips", func(t *testing.T) {
		t.Parallel()
		got := checkRecipeCompatibility(nil, "v0.5.0")
		if got.Status != StatusSkipped {
			t.Fatalf("Status = %v, want %v", got.Status, StatusSkipped)
		}
	})

	t.Run("running version at least the recipe's is ok", func(t *testing.T) {
		t.Parallel()
		r := &recipe.Recipe{CLIVersion: "v0.4.0", RuntimeVersion: "v0.4.0"}
		got := checkRecipeCompatibility(r, "v0.5.0")
		if got.Status != StatusOK {
			t.Fatalf("Status = %v, want %v (Found=%q)", got.Status, StatusOK, got.Found)
		}
	})

	t.Run("running version equal to the recipe's is ok", func(t *testing.T) {
		t.Parallel()
		r := &recipe.Recipe{CLIVersion: "v0.5.0", RuntimeVersion: "v0.5.0"}
		got := checkRecipeCompatibility(r, "v0.5.0")
		if got.Status != StatusOK {
			t.Fatalf("Status = %v, want %v", got.Status, StatusOK)
		}
	})

	t.Run("running version older than the recipe's warns, does not fail", func(t *testing.T) {
		t.Parallel()
		r := &recipe.Recipe{CLIVersion: "v0.9.0", RuntimeVersion: "v0.9.0"}
		got := checkRecipeCompatibility(r, "v0.5.0")
		if got.Status != StatusWarn {
			t.Fatalf("Status = %v, want %v", got.Status, StatusWarn)
		}
		if got.Remedy == "" {
			t.Error("Remedy is empty for a warning check")
		}
	})

	t.Run("running version is dev build, cannot compare, skips", func(t *testing.T) {
		t.Parallel()
		r := &recipe.Recipe{CLIVersion: "v0.5.0", RuntimeVersion: "v0.5.0"}
		got := checkRecipeCompatibility(r, "dev")
		if got.Status != StatusSkipped {
			t.Fatalf("Status = %v, want %v", got.Status, StatusSkipped)
		}
	})

	t.Run("recipe cliVersion is not parsable, warns rather than crashing", func(t *testing.T) {
		t.Parallel()
		r := &recipe.Recipe{CLIVersion: "not-a-version", RuntimeVersion: "v0.5.0"}
		got := checkRecipeCompatibility(r, "v0.5.0")
		if got.Status != StatusWarn {
			t.Fatalf("Status = %v, want %v", got.Status, StatusWarn)
		}
	})
}
