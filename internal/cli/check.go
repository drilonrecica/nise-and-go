package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/check"
	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
	"github.com/drilonrecica/nise-and-go/internal/version"
)

// invariantCheckResult adapts one check.Check to output.Result. The embedded
// type's own JSON tags are reused as-is — an anonymous embed marshals
// identically to the embedded type — so `nise check --json`'s per-check
// documents and check.Check's Go representation cannot drift into two
// subtly different shapes.
//
// One document per check, rather than one aggregate object holding every
// check, follows `nise doctor` and docs/cli-output.md's JSON mode section:
// each check is one discrete event, and a failing run has to end with a
// returned *clierr.Error (Execute's only mechanism for setting an exit
// code), so an aggregate Result would put two competing documents carrying
// overlapping information on the same stdout stream.
type invariantCheckResult struct {
	check.Check
}

// Human renders one check as a short, greppable block: a bracketed status
// tag, the check's name, the invariant it protects, what was actually
// observed, the offending paths, and the remedy. The invariant line is
// printed for every status including skipped — a reader of a skipped check
// has to be told what went unverified, or the skip is indistinguishable
// from a pass.
func (c invariantCheckResult) Human() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", strings.ToUpper(string(c.Status)), c.Name)
	fmt.Fprintf(&b, "\n    invariant: %s", c.Invariant)
	if c.Detail != "" {
		fmt.Fprintf(&b, "\n    detail:    %s", c.Detail)
	}
	for _, p := range c.Paths {
		fmt.Fprintf(&b, "\n    path:      %s", p)
	}
	if c.Remedy != "" {
		fmt.Fprintf(&b, "\n    remedy:    %s", c.Remedy)
	}
	return b.String()
}

// checkCommand implements `nise check` (Nise task M1-012): the invariant
// runner. It enforces the safety, compatibility, migration, and generation
// invariants of BLUEPRINT §5 — and, just as deliberately, nothing else. See
// internal/check for the rule every check is admitted under and
// docs/commands/check.md for the checks that were excluded because they
// would have enforced starter conformity instead.
//
// This file only adapts internal/check's Report to the CLI's output and
// exit-code contract.
func checkCommand() *Command {
	var (
		envFile string
		fix     bool
	)
	return &Command{
		Name:  "check",
		Short: "Verify a project still holds the invariants nise guarantees",
		NewFlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("check", flag.ContinueOnError)
			fs.StringVar(&envFile, "env-file", "", "environment file to validate configuration invariants against")
			// --fix is defined so that typing it produces an honest
			// answer instead of "flag provided but not defined". There
			// is no automatic repair in this slice, and a flag that
			// silently did nothing — or worse, edited files — would be
			// the least recoverable kind of surprise.
			fs.BoolVar(&fix, "fix", false, "not implemented: nise check never edits your project")
			return fs
		},
		Run: func(ctx context.Context, env *Env) error {
			if fix {
				return clierr.New(clierr.ExitError,
					"nise check --fix is not implemented in this version",
					"Run \"nise check\" and apply the remedy each failing check prints. Every remedy is a command or an edit you perform yourself; nise check never writes to your project.",
				).WithCode("check.fix_not_implemented").WithDocs("docs/commands/check.md#fix-is-not-implemented")
			}

			wd, err := os.Getwd()
			if err != nil {
				return clierr.Wrap(err, clierr.ExitError,
					"could not determine the current directory",
					"Retry from a directory you have permission to read.",
				)
			}

			root, found, err := check.FindRoot(wd)
			if err != nil {
				return clierr.Wrap(err, clierr.ExitError,
					"could not search for "+recipe.FileName,
					"Check filesystem permissions for the current directory and its parents.",
				)
			}
			if !found {
				// Every check this command runs is about a project.
				// Reporting nine skips and exiting 0 outside one would
				// be a green result that verified nothing — the exact
				// dishonest-pass this command exists to prevent.
				return clierr.Precondition(
					"no "+recipe.FileName+" found in the current directory or any parent",
					`Run "nise check" from inside a generated project, or create one with "nise new".`,
				).WithCode("check.not_in_project").WithDocs("docs/commands/check.md")
			}

			report := check.Run(ctx, check.Options{
				Root:       root,
				CLIVersion: version.Get().Version,
				EnvFile:    envFile,
			})

			for _, c := range report.Checks {
				env.Out.Result(invariantCheckResult{c})
			}
			ok, failed, skipped := report.Counts()
			env.Out.Line(fmt.Sprintf("%d ok, %d fail, %d skipped", ok, failed, skipped))

			if report.Failed() {
				return clierr.New(clierr.ExitError,
					fmt.Sprintf("%d invariant(s) do not hold in this project", failed),
					`Apply the remedy printed under each failing check, then re-run "nise check".`,
				).WithCode("check.invariant_failed").WithDocs("docs/commands/check.md")
			}
			return nil
		},
	}
}
