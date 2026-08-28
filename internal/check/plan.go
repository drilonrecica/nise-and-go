package check

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// placeholderName and placeholderModulePath stand in when a project's real
// name or module path cannot be recovered.
//
// Recovering them at all is only about app-owned output. No Nise-owned
// template references .AppName or .ModulePath — TestNiseOwnedOutputIgnores
// NameAndModulePath in this package's tests asserts exactly that, so a
// future template that broke the property fails the build rather than
// turning this fallback into a source of phantom regeneration diffs. What
// the name buys is the ownership-header check's coverage of
// cmd/<app>/main.go, whose path is the one generated path that carries the
// application's name.
const (
	placeholderName       = "app"
	placeholderModulePath = "example.com/app"
)

// loadPlan re-renders the project the recipe describes, purely in memory, so
// later checks can ask what Nise owns and what its output should be. It
// returns nil whenever there is no recipe to plan from or the plan itself
// fails, which makes every dependent check skip with that as its reason
// rather than fail on a consequence.
func loadPlan(root string, r *recipe.Recipe) []generator.File {
	if r == nil {
		return nil
	}
	files, err := generator.Plan(generator.Options{
		Name:           projectName(root),
		ModulePath:     projectModulePath(root),
		Profile:        r.Profile,
		Modules:        r.Modules,
		CLIVersion:     r.CLIVersion,
		RuntimeVersion: r.RuntimeVersion,
	})
	if err != nil {
		return nil
	}
	return files
}

// projectName recovers the application name from the project root's own
// directory name, falling back to placeholderName when that is not a name
// `nise new` would have accepted (a renamed or oddly-spelled directory).
func projectName(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return placeholderName
	}
	base := filepath.Base(abs)
	if generator.ValidateName(base) != nil {
		return placeholderName
	}
	return base
}

// projectModulePath recovers the module path from go.mod's module directive,
// falling back to placeholderModulePath. Like projectName it only affects
// app-owned output.
func projectModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, goModFileName)) // #nosec G304 -- root joined with this package's own goModFileName constant, never with external input.
	if err != nil {
		return placeholderModulePath
	}
	for _, raw := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) == 2 && fields[0] == "module" {
			if generator.ValidateModulePath(fields[1]) == nil {
				return fields[1]
			}
			return placeholderModulePath
		}
	}
	return placeholderModulePath
}
