package check

import (
	"context"
	"os"
	"path/filepath"

	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// The stable Check.Name values. They are constants, not literals scattered
// through this package, because a CI job may key off them: renaming one is a
// breaking change to `nise check --json`.
const (
	// CheckRecipe covers the recipe's presence, parseability, and schema
	// version.
	CheckRecipe = "recipe"
	// CheckCLIVersion covers the running CLI against the recipe's
	// cliVersion.
	CheckCLIVersion = "cli-version-compatibility"
	// CheckRuntimeVersion covers the recipe's runtimeVersion against the
	// version go.mod actually requires.
	CheckRuntimeVersion = "runtime-version-consistency"
	// CheckOwnershipHeaders covers the ownership header every generated
	// file declares.
	CheckOwnershipHeaders = "ownership-headers"
	// CheckOwnershipContent covers the embedded content hashes of the two
	// self-verifying Nise-owned files.
	CheckOwnershipContent = "ownership-content-hash"
	// CheckGeneration covers regeneration of every Nise-owned file.
	CheckGeneration = "generation-deterministic"
	// CheckMigrations covers the migration invariants, which arrive with
	// milestone M3.
	CheckMigrations = "migrations"
	// CheckConfig covers the configuration invariants a supplied env file
	// can be validated against without starting the application.
	CheckConfig = "config-invariants"
	// CheckSecrets covers secret material in the project's tracked files.
	CheckSecrets = "secret-material"
)

// Options are the inputs a run is a pure function of, apart from the
// filesystem it reads.
type Options struct {
	// Root is the project root: the directory holding recipe.FileName.
	// Callers get it from FindRoot.
	Root string
	// CLIVersion is the version of the nise binary running the check. It
	// is passed in rather than read here so a test can pin it, exactly as
	// internal/generator does with Options.CLIVersion.
	CLIVersion string
	// EnvFile is the path to an environment file to validate the
	// configuration invariants against. Empty means the configuration
	// check is skipped, with that as its stated reason: there is no
	// default file to guess at, and guessing at ".env" would validate a
	// developer's local file as though it were a production one.
	EnvFile string
	// Git, when non-nil, lists a directory's git-tracked files. It is a
	// parameter so the secret-material check is testable without a git
	// repository. A nil value means the real implementation (GitTracked).
	Git TrackedLister
}

// TrackedLister reports the git-tracked files under a directory, relative
// to it and slash-separated. found is false when dir is not inside a git
// work tree or git is unavailable — which is not an error: a generated
// project is not a git repository until the developer makes it one, and
// `nise new` deliberately never runs git.
type TrackedLister func(ctx context.Context, dir string) (files []string, found bool, err error)

// FindRoot walks upward from start to the first ancestor containing
// recipe.FileName, which is internal/recipe's documented project-root
// convention. It stops at the filesystem root rather than looping.
//
// This duplicates the identical walk in internal/doctor, which keeps its
// own copy unexported. Fifteen lines of upward stat loop are the cheaper
// duplication: the alternative is a new shared package that both command
// implementations would have to import, for one function whose behavior is
// fixed by internal/recipe's own documented convention and cannot drift
// without that convention changing first.
func FindRoot(start string) (dir string, found bool, err error) {
	dir = start
	for {
		_, statErr := os.Stat(filepath.Join(dir, recipe.FileName))
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

// Run evaluates every invariant check against the project at opts.Root and
// returns them in a fixed order.
//
// Checks that depend on a valid recipe are skipped, not failed, when the
// recipe itself did not load: reporting five failures for one broken file
// would bury the one finding that matters under four consequences of it.
func Run(ctx context.Context, opts Options) Report {
	checks := make([]Check, 0, 9)

	recipeCheck, r := checkRecipe(opts.Root)
	checks = append(checks,
		recipeCheck,
		checkCLIVersion(r, opts.CLIVersion),
		checkRuntimeVersion(opts.Root, r),
	)

	plan := loadPlan(opts.Root, r)
	checks = append(checks,
		checkOwnershipHeaders(opts.Root, plan),
		checkOwnershipContent(opts.Root, r),
		checkGeneration(opts.Root, r, plan, opts.CLIVersion),
		checkMigrations(),
		checkConfig(opts.EnvFile),
		checkSecrets(ctx, opts.Root, opts.Git),
	)

	return Report{Checks: checks}
}
