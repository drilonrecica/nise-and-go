package cli

import (
	"context"

	"github.com/drilonrecica/nise-and-go/internal/version"
)

// Commands returns the top-level command registry, in the exact order they
// should be listed in help output. This is the one place a task adds a
// command: append a new *Command (built by its own small constructor
// function, following the versionCommand pattern below) to the returned
// slice, or add a *Command to an existing entry's Subcommands for nesting
// (e.g. a future "db" entry gaining a Subcommands slice holding "migrate",
// "backup", "restore", "verify-backup").
//
// Commands is a plain function called fresh by Execute on every dispatch —
// never a package-level variable populated by init() or by a side effect of
// importing a command's package — so there is no registration order to get
// wrong and no state a first dispatch could leave behind for a second one
// in the same process (relevant for tests, which call Execute repeatedly).
func Commands() []*Command {
	return []*Command{
		versionCommand(),
		newCommand(),
		doctorCommand(),
		agentsCommand(),
		testCommand(),
		devCommand(),
		generateCommand(),
		checkCommand(),
	}
}

// versionResult adapts version.Info (already JSON-tagged by internal/
// version) to output.Result by giving it a human rendering, reusing
// version.Info.String() so `nise version` and `nise --json version` report
// the exact same underlying data.
type versionResult struct {
	version.Info
}

func (v versionResult) Human() string { return v.String() }

func versionCommand() *Command {
	return &Command{
		Name:  "version",
		Short: "Print the nise CLI version",
		Run: func(_ context.Context, env *Env) error {
			env.Out.Result(versionResult{version.Get()})
			return nil
		},
	}
}
