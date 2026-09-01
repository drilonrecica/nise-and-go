package upgrade

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// goModFileName is the file whose framework requirement an upgrade bumps.
const goModFileName = "go.mod"

// Action is what an upgrade will do to one file.
type Action string

const (
	// ActionUpdate rewrites an existing Nise-owned file.
	ActionUpdate Action = "update"
	// ActionCreate writes a Nise-owned file the project does not have yet,
	// which is how a new subsystem's generated wiring arrives.
	ActionCreate Action = "create"
	// ActionRemove deletes a Nise-owned file the new version no longer plans.
	// An orphaned .gen.go from a subsystem that no longer exists does not stop
	// mattering when it stops being written — it still compiles, and still
	// refers to things that may not.
	ActionRemove Action = "remove"
)

// FileChange is one file an upgrade will write or delete.
type FileChange struct {
	Path   string `json:"path"`
	Action Action `json:"action"`
}

// CodemodChange is one declared rewrite, and where it matched.
type CodemodChange struct {
	Path        string `json:"path"`
	Describe    string `json:"describe"`
	Occurrences int    `json:"occurrences"`
}

// Report is everything an upgrade will do, or has done. Plan returns it
// without writing anything; Apply returns the same value after writing, so
// what a developer reads afterwards is what was checked beforehand.
type Report struct {
	// CLIFrom and CLITo are the nise that created the project and the nise
	// running now.
	CLIFrom string `json:"cliFrom"`
	CLITo   string `json:"cliTo"`
	// RuntimeFrom and RuntimeTo are the framework versions.
	RuntimeFrom string `json:"runtimeFrom"`
	RuntimeTo   string `json:"runtimeTo"`
	// UpToDate reports that there is nothing to do. It is a distinct field
	// rather than an empty Files slice, because "nothing changed" and
	// "nothing was planned" are different answers.
	UpToDate bool `json:"upToDate"`
	// Files are the Nise-owned files to rewrite, create, or remove, sorted by
	// path. Application-owned files never appear here.
	Files []FileChange `json:"files,omitempty"`
	// Dependencies are the pinned requirements to move, in go.mod and in the
	// generated frontend's package.json. Only forward, and only the ones nise
	// pins: a dependency the application removed stays removed, and one it
	// pinned ahead of nise stays ahead, with a note.
	Dependencies []RequireChange `json:"dependencies,omitempty"`
	// Codemods are the declared rewrites that matched application-owned Go
	// source, with the file and count of each. A codemod that matched nothing
	// is not listed: an entry here is a change to review.
	Codemods []CodemodChange `json:"codemods,omitempty"`
	// Notes are the changes a person has to make. Printed, never applied.
	Notes []string `json:"notes,omitempty"`
	// NextSteps are the commands to run afterwards. `go mod tidy` is here
	// rather than run, because resolving a module graph reaches the network
	// and an upgrade should not be the command that discovers a dependency is
	// unreachable.
	NextSteps []string `json:"nextSteps,omitempty"`
}

// Options are the inputs a plan is computed from.
type Options struct {
	// Root is the project root: the directory holding nise.json.
	Root string
	// CLIVersion is the running nise's version, supplied rather than read so
	// a test can pin it.
	CLIVersion string
	// RuntimeVersion is the framework version this nise ships. An empty value
	// defaults to what the generator requires.
	RuntimeVersion string
	// Codemods enables the declared textual rewrites over application-owned
	// Go source. Disabling them leaves every such change as a note, which is
	// the escape hatch for a developer who would rather do it by hand.
	Codemods bool
	// Migrations overrides the declared migration table. Nil uses the real
	// one; tests supply their own so the machinery is exercised without
	// waiting for a second release to exist.
	Migrations []Migration
}

// ErrNoRecipe reports that the directory is not a generated project.
var ErrNoRecipe = errors.New("no project recipe")

// Downgrade reports that the running nise is older than the one the project
// records. It is its own error because the correct response is not to try
// harder: an older nise cannot know what a newer one wrote, and regenerating
// with it would quietly revert files.
type Downgrade struct {
	Running, Recorded string
}

func (d *Downgrade) Error() string {
	return fmt.Sprintf("this nise is %s and the project was last touched by %s", d.Running, d.Recorded)
}

// Plan computes the upgrade without writing anything.
func Plan(opts Options) (Report, error) {
	if opts.RuntimeVersion == "" {
		opts.RuntimeVersion = generator.NiseModuleVersion
	}

	current, err := readRecipe(opts.Root)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		CLIFrom:     current.CLIVersion,
		CLITo:       opts.CLIVersion,
		RuntimeFrom: current.RuntimeVersion,
		RuntimeTo:   opts.RuntimeVersion,
	}

	target := current
	target.CLIVersion = opts.CLIVersion
	target.RuntimeVersion = opts.RuntimeVersion

	planned, err := planFiles(opts.Root, target)
	if err != nil {
		return Report{}, err
	}

	chain, missing := path(current.RuntimeVersion, opts.RuntimeVersion, migrationTable(opts))
	report.Files = fileChanges(opts.Root, planned, declaredRemovals(chain))

	dependencies, dependencyNotes, err := planDependencies(opts.Root)
	if err != nil {
		return Report{}, err
	}
	report.Dependencies = dependencies
	report.Notes = append(report.Notes, dependencyNotes...)

	for _, gap := range missing {
		report.Notes = append(report.Notes, fmt.Sprintf(
			"No migration is declared from %s, so nothing was applied for the versions after it. "+
				"Read the release notes between %s and %s before relying on this upgrade.",
			gap, gap, opts.RuntimeVersion))
	}
	for _, migration := range chain {
		if migration.Summary != "" {
			report.Notes = append(report.Notes, migration.From+" → "+migration.To+": "+migration.Summary)
		}
		report.Notes = append(report.Notes, migration.Notes...)
	}

	if opts.Codemods {
		codemods, err := planCodemods(opts.Root, planned, chain)
		if err != nil {
			return Report{}, err
		}
		report.Codemods = codemods
	} else {
		for _, migration := range chain {
			for _, mod := range migration.Codemods {
				report.Notes = append(report.Notes,
					"Codemods are disabled, so this was not applied: "+mod.Describe+
						" (replace "+mod.Old+" with "+mod.New+")")
			}
		}
	}

	report.UpToDate = len(report.Files) == 0 && len(report.Dependencies) == 0 &&
		len(report.Codemods) == 0 && len(report.Notes) == 0 &&
		current.CLIVersion == opts.CLIVersion
	if !report.UpToDate {
		report.NextSteps = nextSteps(report)
	}
	return report, nil
}

// migrationTable returns the migrations a plan should use.
func migrationTable(opts Options) []Migration {
	if opts.Migrations != nil {
		return opts.Migrations
	}
	return migrations
}

// nextSteps are the commands the developer runs after an upgrade, in order.
func nextSteps(report Report) []string {
	var steps []string
	touched := map[string]bool{}
	for _, change := range report.Dependencies {
		touched[change.File] = true
	}
	// Deliberately not run: resolving a module graph or a lockfile reaches the
	// network, and an upgrade must not be the command that discovers a
	// dependency is unreachable — least of all halfway through writing files.
	if touched[goModFileName] {
		steps = append(steps, "go mod tidy")
	}
	if touched[packageJSONFileName] {
		steps = append(steps, "pnpm --dir frontend install")
	}
	// git diff comes before the checks because reviewing what changed is the
	// point of an upgrade that never overwrites application code.
	return append(steps, "git diff", "nise check", "go build ./...")
}

func readRecipe(root string) (recipe.Recipe, error) {
	data, err := os.ReadFile(filepath.Join(root, recipe.FileName)) // #nosec G304 -- root joined with the recipe package's own file name constant.
	if errors.Is(err, os.ErrNotExist) {
		return recipe.Recipe{}, ErrNoRecipe
	}
	if err != nil {
		return recipe.Recipe{}, fmt.Errorf("reading %s: %w", recipe.FileName, err)
	}
	return recipe.Unmarshal(data)
}

// planFiles renders the project a recipe describes, in memory.
//
// The name and module path are recovered from the project rather than from the
// recipe, which does not record them. They only affect application-owned
// output, which an upgrade never writes — but a plan built with the wrong ones
// would still report phantom changes, so they are recovered rather than
// assumed.
func planFiles(root string, r recipe.Recipe) ([]generator.File, error) {
	files, err := generator.Plan(generator.Options{
		Name:           projectName(root),
		ModulePath:     projectModulePath(root),
		Profile:        r.Profile,
		Modules:        r.Modules,
		CLIVersion:     r.CLIVersion,
		RuntimeVersion: r.RuntimeVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("planning the project at %s: %w", r.RuntimeVersion, err)
	}
	return files, nil
}

// declaredRemovals collects the Nise-owned files the migrations in a chain
// declare as no longer written.
func declaredRemovals(chain []Migration) []string {
	var removed []string
	for _, migration := range chain {
		removed = append(removed, migration.Removed...)
	}
	return removed
}

// fileChanges compares what is on disk against what the new version plans,
// over Nise-owned files only.
//
// Application-owned files are excluded before anything else happens, and that
// exclusion is the single most important line in this package: an upgrade that
// regenerates an app-owned file has not upgraded the project, it has discarded
// the application.
func fileChanges(root string, planned []generator.File, removed []string) []FileChange {
	var changes []FileChange

	plannedByPath := make(map[string]generator.File, len(planned))
	for _, file := range planned {
		if file.Owner != generator.OwnerNise {
			continue
		}
		plannedByPath[file.Path] = file

		onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path))) // #nosec G304 -- root joined with a path this generator planned.
		switch {
		case errors.Is(err, os.ErrNotExist):
			changes = append(changes, FileChange{Path: file.Path, Action: ActionCreate})
		case err != nil:
			// Unreadable is reported as an update rather than swallowed: the
			// write will surface the real error, and skipping it here would
			// hide a file from the plan a developer reviews.
			changes = append(changes, FileChange{Path: file.Path, Action: ActionUpdate})
		case string(onDisk) != string(file.Content):
			changes = append(changes, FileChange{Path: file.Path, Action: ActionUpdate})
		}
	}

	// Files a migration declares this version stopped writing. Two refusals
	// guard the only place this package deletes anything:
	//
	//   - a path the new version still plans is a contradiction in the
	//     migration table, and deleting a file about to be written would
	//     succeed silently and mean nothing;
	//   - a path that is not a plain relative path inside the project is not a
	//     path nise ever wrote, whatever the table says.
	for _, candidate := range removed {
		if _, still := plannedByPath[candidate]; still {
			continue
		}
		if !insideProject(candidate) {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); err == nil {
			changes = append(changes, FileChange{Path: candidate, Action: ActionRemove})
		}
	}

	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

// planCodemods finds where the declared rewrites match application-owned Go
// source.
//
// The set of files it may touch comes from the *plan*, not from a directory
// walk: a codemod is allowed to edit a file nise generated once and handed to
// the application, and nothing else. A walk would also reach vendored code,
// node_modules, and whatever else the developer has put in the tree.
func planCodemods(root string, planned []generator.File, chain []Migration) ([]CodemodChange, error) {
	var changes []CodemodChange
	for _, file := range planned {
		if file.Owner != generator.OwnerApp || !strings.HasSuffix(file.Path, ".go") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path))) // #nosec G304 -- root joined with a path this generator planned.
		if errors.Is(err, os.ErrNotExist) {
			// The application deleted it, which is its prerogative.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", file.Path, err)
		}
		text := string(source)
		for _, migration := range chain {
			for _, mod := range migration.Codemods {
				rewritten, count := mod.apply(text)
				if count == 0 {
					continue
				}
				text = rewritten
				changes = append(changes, CodemodChange{
					Path: file.Path, Describe: mod.Describe, Occurrences: count,
				})
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path != changes[j].Path {
			return changes[i].Path < changes[j].Path
		}
		return changes[i].Describe < changes[j].Describe
	})
	return changes, nil
}

// projectName recovers the application's name from the project root's own
// directory name, and projectModulePath the module path from go.mod. Both fall
// back to a placeholder.
//
// Neither affects a Nise-owned file — no Nise-owned template references either
// (internal/check asserts exactly that) — so an upgrade would write the same
// bytes with the wrong values. They are recovered anyway, because a plan built
// with the wrong ones reports phantom changes to app-owned files, and this
// package uses the app-owned half of the plan to decide which files a codemod
// may touch.
// insideProject reports whether a declared removal is a plain relative path
// that stays inside the project. The table is this repository's own, so this
// is not defending against an attacker — it is making a typo in a hand-written
// list fail to delete something rather than delete the wrong thing.
func insideProject(candidate string) bool {
	if candidate == "" || strings.HasPrefix(candidate, "/") || filepath.IsAbs(candidate) {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(candidate))
	return cleaned == candidate && cleaned != "." && !strings.HasPrefix(cleaned, "../")
}

func projectName(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "app"
	}
	base := filepath.Base(abs)
	if generator.ValidateName(base) != nil {
		return "app"
	}
	return base
}

func projectModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, goModFileName)) // #nosec G304 -- root joined with this package's own goModFileName constant.
	if err != nil {
		return "example.com/app"
	}
	for _, raw := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) == 2 && fields[0] == "module" {
			if generator.ValidateModulePath(fields[1]) == nil {
				return fields[1]
			}
			return "example.com/app"
		}
	}
	return "example.com/app"
}
