package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/drilonrecica/nise-and-go/internal/agentsfile"
	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// findProjectRoot walks upward from start looking for a directory
// containing recipe.FileName, matching internal/recipe's own documented
// project-root convention. This duplicates internal/doctor's own
// unexported findRecipeRoot rather than extracting a shared helper: this
// task's brief explicitly rules out touching internal/doctor (a review of
// that task is running concurrently), and the two copies are small enough
// (about a dozen lines) that duplicating them is cheaper and safer than
// coordinating a shared-package change across two tasks mid-review. See
// internal/cli/clierr/redact.go's doc comment for the same tradeoff made
// elsewhere in this codebase. If the two ever drift, that is an accepted
// cost of the two tasks' isolation, not a bug in either copy.
func findProjectRoot(start string) (dir string, found bool, err error) {
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

// agentsResult adapts agentsfile.Result to output.Result.
type agentsResult struct {
	agentsfile.Result
}

func (r agentsResult) Human() string {
	return fmt.Sprintf("wrote %s\nwrote %s", r.AgentsPath, r.ArchitecturePath)
}

// agentsCommand implements `nise agents` (Nise task M1-015): it writes a
// static AGENTS.md and a machine-readable .nise/architecture.json into the
// generated project rooted at (or above) the current directory, deriving
// both entirely from the project's recipe. See internal/agentsfile for the
// generation and refusal-to-overwrite logic; this file only adapts that
// package to the CLI's output and exit-code contract, the same shape
// doctor.go uses over internal/doctor.
//
// It is a static artifact generator: no AI, no network call, no external
// tool invocation (privateDocs/DECISIONS.md's AI section). Both outputs
// are Nise-owned; a hand-edited copy of either is never silently
// overwritten (see internal/agentsfile.ConflictError) unless --force is
// given.
func agentsCommand() *Command {
	var force bool
	return &Command{
		Name:  "agents",
		Short: "Generate AGENTS.md and the architecture map for external coding tools",
		NewFlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("agents", flag.ContinueOnError)
			fs.BoolVar(&force, "force", false, "overwrite generated files even if they were hand-edited")
			return fs
		},
		Run: func(_ context.Context, env *Env) error {
			wd, err := os.Getwd()
			if err != nil {
				return clierr.Wrap(err, clierr.ExitError,
					"could not determine the current directory",
					"Retry from a directory you have permission to read.",
				)
			}

			root, found, err := findProjectRoot(wd)
			if err != nil {
				return clierr.Wrap(err, clierr.ExitError,
					fmt.Sprintf("could not search for %s", recipe.FileName),
					"Check filesystem permissions for the current directory and its parents.",
				).WithDocs("docs/commands/agents.md")
			}
			if !found {
				return clierr.Precondition(
					fmt.Sprintf("no %s found in the current directory or any parent", recipe.FileName),
					`Run "nise new" to create a project, or run "nise agents" from inside one.`,
				).WithCode("agents.no_project").WithDocs("docs/commands/agents.md")
			}

			r, err := recipe.Load(os.DirFS(root), recipe.FileName)
			if err != nil {
				return clierr.Wrap(err, clierr.ExitPrecondition,
					fmt.Sprintf("%s does not parse", filepath.Join(root, recipe.FileName)),
					"Fix nise.json by hand, or restore it from version control; see docs/adr/0010-project-recipe-format.md.",
				).WithCode("agents.invalid_recipe").WithDocs("docs/commands/agents.md")
			}

			result, err := agentsfile.Write(root, r, force)
			if err != nil {
				return renderAgentsWriteError(env, err)
			}

			env.Out.Result(agentsResult{*result})
			return nil
		},
	}
}

// renderAgentsWriteError turns an error from agentsfile.Write into the
// command's returned *clierr.Error. A *agentsfile.ConflictError gets a
// preview of every conflicting file's current content printed as
// informational output (visible in human mode; a no-op in --json, where
// the same previews instead reach the caller through the error's
// structured "details" — see docs/cli-output.md's JSON rendering section)
// before the summarizing error itself is returned.
func renderAgentsWriteError(env *Env, err error) error {
	var conflictErr *agentsfile.ConflictError
	if errors.As(err, &conflictErr) {
		ce := clierr.New(clierr.ExitError,
			fmt.Sprintf("%d generated file(s) would be overwritten but do not match nise's last generated content",
				len(conflictErr.Conflicts)),
			"Re-run with --force to overwrite, discarding the hand-edited content shown below.",
		).WithCode("agents.conflict").WithDocs("docs/commands/agents.md")

		for _, c := range conflictErr.Conflicts {
			env.Out.Line(fmt.Sprintf("--- %s (current content that would be lost) ---\n%s", c.Path, c.Preview))
			ce = ce.WithDetail(c.Path, c.Preview)
		}
		return ce
	}

	return clierr.Wrap(err, clierr.ExitError,
		"could not write AGENTS.md and the architecture map",
		"Check filesystem permissions and retry.",
	).WithDocs("docs/commands/agents.md")
}
