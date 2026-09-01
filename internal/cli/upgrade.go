package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/check"
	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/doctor"
	"github.com/drilonrecica/nise-and-go/internal/upgrade"
	"github.com/drilonrecica/nise-and-go/internal/version"
)

const upgradeDocs = "docs/commands/upgrade.md"

// upgradeResult renders an upgrade report.
type upgradeResult struct {
	upgrade.Report
	// DryRun reports that nothing was written.
	DryRun bool `json:"dryRun"`
}

func (r upgradeResult) Human() string {
	var b strings.Builder
	if r.UpToDate {
		fmt.Fprintf(&b, "This project is already on nise %s. Nothing to do.", r.CLITo)
		return b.String()
	}

	verb := "Upgraded"
	if r.DryRun {
		verb = "Would upgrade"
	}
	fmt.Fprintf(&b, "%s from nise %s (runtime %s) to %s (runtime %s).\n",
		verb, r.CLIFrom, r.RuntimeFrom, r.CLITo, r.RuntimeTo)

	if len(r.Files) > 0 {
		fmt.Fprintf(&b, "\nGenerated files (%d):\n", len(r.Files))
		for _, file := range r.Files {
			fmt.Fprintf(&b, "  %-7s %s\n", file.Action, file.Path)
		}
	}
	if len(r.Dependencies) > 0 {
		fmt.Fprintf(&b, "\nDependencies (%d):\n", len(r.Dependencies))
		for _, dep := range r.Dependencies {
			fmt.Fprintf(&b, "  %s: %s %s → %s\n", dep.File, dep.Module, dep.From, dep.To)
		}
	}
	if len(r.Codemods) > 0 {
		fmt.Fprintf(&b, "\nCode rewrites (%d):\n", len(r.Codemods))
		for _, mod := range r.Codemods {
			fmt.Fprintf(&b, "  %s (%d) — %s\n", mod.Path, mod.Occurrences, mod.Describe)
		}
	}
	// Notes come last of the four, because they are the ones a person has to
	// act on and the last thing printed is the thing that gets read.
	if len(r.Notes) > 0 {
		fmt.Fprintf(&b, "\nFor you to do (%d):\n", len(r.Notes))
		for _, note := range r.Notes {
			fmt.Fprintf(&b, "  - %s\n", note)
		}
	}
	if len(r.NextSteps) > 0 {
		b.WriteString("\nNext:\n")
		for _, step := range r.NextSteps {
			fmt.Fprintf(&b, "  %s\n", step)
		}
	}

	if r.DryRun {
		b.WriteString("\nNothing was written. Run without --dry-run to apply it.")
	} else {
		b.WriteString("\nNo application-owned file was regenerated. Review the diff before committing.")
	}
	return b.String()
}

func upgradeCommand() *Command {
	var (
		dryRun     bool
		noCodemods bool
		force      bool
	)
	return &Command{
		Name:  "upgrade",
		Short: "Move this project to the nise that is running",
		NewFlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
			fs.BoolVar(&dryRun, "dry-run", false, "report what would change and write nothing")
			fs.BoolVar(&noCodemods, "no-codemods", false, "never rewrite application source; report every such change as a note instead")
			fs.BoolVar(&force, "force", false, "upgrade even though the working tree has uncommitted changes")
			return fs
		},
		Run: func(ctx context.Context, env *Env) error {
			root, err := upgradeRoot()
			if err != nil {
				return err
			}
			if !dryRun && !force {
				if err := refuseDirtyTree(ctx, root); err != nil {
					return err
				}
			}

			opts := upgrade.Options{
				Root:       root,
				CLIVersion: version.Get().Version,
				Codemods:   !noCodemods,
			}

			// Plan first, always. It writes nothing, and it is the only way
			// to learn which nise last touched the project without this
			// command growing its own recipe reader.
			report, err := upgrade.Plan(opts)
			if err != nil {
				return upgradeError(err)
			}
			if err := refuseDowngrade(opts.CLIVersion, report.CLIFrom); err != nil {
				return upgradeError(err)
			}
			if !dryRun {
				if report, err = upgrade.Apply(opts); err != nil {
					return upgradeError(err)
				}
			}
			env.Out.Result(upgradeResult{Report: report, DryRun: dryRun})
			return nil
		},
	}
}

func upgradeRoot() (string, error) {
	working, err := os.Getwd()
	if err != nil {
		return "", clierr.Wrap(err, clierr.ExitError,
			"nise could not determine the current directory",
			"Run nise upgrade from inside a generated project.").WithCode("upgrade.no_working_directory")
	}
	root, found, err := check.FindRoot(working)
	if err != nil {
		return "", clierr.Wrap(err, clierr.ExitError,
			"nise could not look for a project root",
			"Check the directory is readable.").WithCode("upgrade.root_unreadable")
	}
	if !found {
		return "", clierr.Precondition(
			"no nise.json found in this directory or any parent, so there is no project to upgrade",
			"Run nise upgrade from inside a project created by nise new.",
		).WithCode("upgrade.not_a_project").WithDocs(upgradeDocs)
	}
	return root, nil
}

// refuseDirtyTree stops an upgrade that would mix its changes with the
// developer's.
//
// This is the whole review mechanism. An upgrade regenerates files, bumps
// dependencies, and rewrites imports; the way a person checks any of that is
// `git diff`, and a diff that also contains half a feature they were in the
// middle of is not one anybody reads. Committing first costs a minute and
// makes the upgrade a single revertible commit.
//
// A project that is not a git repository is not refused. `nise new` never runs
// git, and refusing to upgrade a project the developer has not chosen to
// version would be inventing a requirement.
func refuseDirtyTree(ctx context.Context, root string) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil
	}
	// #nosec G204 -- git is the absolute path exec.LookPath resolved, every
	// argument but one is a fixed literal, and the remaining one is the
	// project root this command already resolved from the working directory.
	cmd := exec.CommandContext(ctx, git, "-C", root, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		// Not a git repository, or git could not run. Either way there is no
		// working tree to be dirty.
		return nil
	}
	changed := strings.TrimSpace(string(out))
	if changed == "" {
		return nil
	}
	return clierr.Precondition(
		"the working tree has uncommitted changes, and an upgrade's diff is how you review it",
		"Commit or stash your work first, then run nise upgrade again. Use --dry-run to see what it would do, or --force to mix the two.",
	).WithCode("upgrade.dirty_tree").WithDocs(upgradeDocs).
		WithDetail("changedFiles", fmt.Sprint(len(strings.Split(changed, "\n"))))
}

func upgradeError(err error) error {
	if errors.Is(err, upgrade.ErrNoRecipe) {
		return clierr.Wrap(err, clierr.ExitPrecondition,
			"this directory has no project recipe, so nise cannot tell what to upgrade",
			"Run nise upgrade from inside a project created by nise new.",
		).WithCode("upgrade.not_a_project").WithDocs(upgradeDocs)
	}
	var downgrade *upgrade.Downgrade
	if errors.As(err, &downgrade) {
		return clierr.Wrap(err, clierr.ExitPrecondition,
			"this nise is older than the one that last touched the project",
			"Install the newer nise and run it there. An older nise cannot know what a newer one wrote, and would quietly revert files.",
		).WithCode("upgrade.downgrade").WithDocs(upgradeDocs)
	}
	return clierr.Wrap(err, clierr.ExitError,
		"the upgrade could not be completed",
		"Run nise upgrade --dry-run to see what it was trying to do, and check the project is readable and writable.",
	).WithCode("upgrade.failed").WithDocs(upgradeDocs)
}

// refuseDowngrade stops an older nise from regenerating a project a newer one
// wrote.
//
// It lives here rather than in internal/upgrade because comparing two version
// strings needs a parser, and the one this CLI already has is
// internal/doctor's — making the upgrade package depend on doctor to answer a
// question the CLI already knows the answer to would be the wrong direction
// for one comparison.
func refuseDowngrade(running, recorded string) error {
	runningVersion, err := doctor.ParseVersion(running)
	if err != nil {
		// A build from a checkout reports "dev", which no parser can compare.
		// Refusing there would make the framework's own development loop
		// unusable, and the ownership rules protect the project either way.
		return nil
	}
	recordedVersion, err := doctor.ParseVersion(recorded)
	if err != nil {
		return nil
	}
	if runningVersion.AtLeast(recordedVersion) {
		return nil
	}
	return &upgrade.Downgrade{Running: running, Recorded: recorded}
}
