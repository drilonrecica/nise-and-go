package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// CheckRecipeName and CheckRecipeCompatibilityName are the stable Check.Name
// values the two recipe-related checks report under.
const (
	CheckRecipeName              = "recipe"
	CheckRecipeCompatibilityName = "recipe-compatibility"
)

// findRecipeRoot walks upward from start looking for a directory
// containing recipe.FileName, matching internal/recipe's own documented
// project-root convention ("Commands find the project root by walking up
// from the working directory to the first ancestor containing this
// file."). It stops, reporting not found, once it reaches a directory
// whose parent is itself (the filesystem root) rather than looping
// forever.
func findRecipeRoot(start string) (dir string, found bool, err error) {
	dir = start
	for {
		candidate := filepath.Join(dir, recipe.FileName)
		_, statErr := os.Stat(candidate)
		switch {
		case statErr == nil:
			return dir, true, nil
		case os.IsNotExist(statErr):
			// keep walking up
		default:
			return "", false, statErr
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
		dir = parent
	}
}

// checkRecipe looks for a project recipe at or above workDir and, when one
// is found, verifies it parses. It returns the check result and the parsed
// recipe (nil when there is no recipe to compare, or it failed to parse),
// so the caller can feed the same result into checkRecipeCompatibility
// without loading the file twice.
func checkRecipe(workDir string) (Check, *recipe.Recipe) {
	const required = "a valid " + recipe.FileName + " (only required inside a generated Nise project)"

	root, found, err := findRecipeRoot(workDir)
	if err != nil {
		return Check{
			Name:     CheckRecipeName,
			Status:   StatusFail,
			Found:    fmt.Sprintf("could not search for %s: %s", recipe.FileName, err),
			Required: required,
			Remedy:   "Check filesystem permissions for the current directory and its parents.",
		}, nil
	}
	if !found {
		return Check{
			Name:     CheckRecipeName,
			Status:   StatusSkipped,
			Found:    fmt.Sprintf("no %s found in the current directory or any parent", recipe.FileName),
			Required: required,
		}, nil
	}

	path := filepath.Join(root, recipe.FileName)
	r, err := recipe.Load(os.DirFS(root), recipe.FileName)
	if err != nil {
		return Check{
			Name:     CheckRecipeName,
			Status:   StatusFail,
			Found:    fmt.Sprintf("%s: %s", path, err),
			Required: required,
			Remedy:   recipeRemedy(err),
		}, nil
	}

	return Check{
		Name:   CheckRecipeName,
		Status: StatusOK,
		Found: fmt.Sprintf("%s (schemaVersion %d, profile %s, cliVersion %s, runtimeVersion %s)",
			path, r.SchemaVersion, r.Profile, r.CLIVersion, r.RuntimeVersion),
		Required: required,
	}, &r
}

// recipeRemedy picks the recovery action for a recipe that failed to
// parse: upgrading nise for a recipe written by a newer schema version, or
// fixing/regenerating the file for anything else.
func recipeRemedy(err error) string {
	var schemaErr *recipe.SchemaVersionError
	if errors.As(err, &schemaErr) {
		return fmt.Sprintf(
			"Upgrade nise: this recipe declares schema version %d, but this build only understands up to %d.",
			schemaErr.Found, schemaErr.Supported,
		)
	}
	return "Fix " + recipe.FileName + " by hand, or restore it from version control; see docs/adr/0010-project-recipe-format.md."
}

// checkRecipeCompatibility compares the recipe's recorded cliVersion
// against runningVersion (the currently executing nise build's own
// version). It reports StatusSkipped, not StatusOK, whenever it cannot
// actually verify compatibility — no recipe to compare, or either version
// string does not parse — because reporting ok without having compared
// anything is exactly the "honest status" failure this package exists to
// avoid.
func checkRecipeCompatibility(r *recipe.Recipe, runningVersion string) Check {
	if r == nil {
		return Check{
			Name:     CheckRecipeCompatibilityName,
			Status:   StatusSkipped,
			Found:    "no recipe to compare (see the \"" + CheckRecipeName + "\" check)",
			Required: "only evaluated inside a generated Nise project with a valid recipe",
		}
	}

	required := fmt.Sprintf("nise >= the recipe's cliVersion (%s)", r.CLIVersion)

	running, err := ParseVersion(runningVersion)
	if err != nil {
		return Check{
			Name:     CheckRecipeCompatibilityName,
			Status:   StatusSkipped,
			Found:    fmt.Sprintf("this nise build reports version %q, which is not a comparable version", runningVersion),
			Required: required,
		}
	}

	recipeVersion, err := ParseVersion(r.CLIVersion)
	if err != nil {
		return Check{
			Name:     CheckRecipeCompatibilityName,
			Status:   StatusWarn,
			Found:    fmt.Sprintf("recipe cliVersion %q is not a comparable version", r.CLIVersion),
			Required: "a comparable cliVersion",
			Remedy:   "Check that " + recipe.FileName + " was not hand-edited; regenerate it if unsure.",
		}
	}

	if running.AtLeast(recipeVersion) {
		return Check{
			Name:     CheckRecipeCompatibilityName,
			Status:   StatusOK,
			Found:    fmt.Sprintf("running nise %s; recipe runtimeVersion %s", running.String(), r.RuntimeVersion),
			Required: required,
		}
	}

	return Check{
		Name:     CheckRecipeCompatibilityName,
		Status:   StatusWarn,
		Found:    "running nise " + running.String(),
		Required: required,
		Remedy:   "This project was created or last upgraded with a newer nise (" + recipeVersion.String() + "). Upgrade the nise binary you are running to at least that version.",
	}
}
