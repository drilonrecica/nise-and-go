package cli

import (
	"flag"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/cli/output"
)

// commandSummary is one row of a command listing: a name and its one-line
// help.
type commandSummary struct {
	Name  string `json:"name"`
	Short string `json:"short"`
}

// flagSummary is one row of a command's own flag listing, drawn from its
// flag.FlagSet.
type flagSummary struct {
	Name    string `json:"name"`
	Default string `json:"default"`
	Usage   string `json:"usage"`
}

// helpResult is the output.Result for both root help ("nise help" / bare
// "nise") and command-specific help ("nise <path...> --help"). Path is
// empty for root help. Commands lists either the top-level registry (root
// help) or a command's own Subcommands (a group command's help); it is
// empty for a leaf command that takes no subcommands.
type helpResult struct {
	Path     []string         `json:"path,omitempty"`
	Short    string           `json:"short,omitempty"`
	Usage    string           `json:"usage"`
	Commands []commandSummary `json:"commands,omitempty"`
	Flags    []flagSummary    `json:"flags,omitempty"`
}

func (h helpResult) Human() string {
	var b strings.Builder
	if len(h.Path) == 0 {
		b.WriteString("nise — the Nise & Go project CLI\n\n")
	} else {
		b.WriteString(strings.Join(append([]string{"nise"}, h.Path...), " "))
		if h.Short != "" {
			b.WriteString(" — " + h.Short)
		}
		b.WriteString("\n\n")
	}
	b.WriteString("Usage:\n  ")
	b.WriteString(h.Usage)
	b.WriteString("\n")

	if len(h.Flags) > 0 {
		b.WriteString("\nFlags:\n")
		for _, f := range h.Flags {
			b.WriteString("  --" + f.Name)
			if f.Default != "" {
				b.WriteString(" (default " + f.Default + ")")
			}
			b.WriteString("\n      " + f.Usage + "\n")
		}
	}

	if len(h.Commands) > 0 {
		b.WriteString("\nCommands:\n")
		width := 0
		for _, c := range h.Commands {
			if len(c.Name) > width {
				width = len(c.Name)
			}
		}
		for _, c := range h.Commands {
			b.WriteString("  " + c.Name + strings.Repeat(" ", width-len(c.Name)+2) + c.Short + "\n")
		}
		b.WriteString("\nRun \"" + strings.Join(append(append([]string{"nise"}, h.Path...), "<command>", "--help"), " ") + "\" for more information about a command.\n")
	}

	b.WriteString("\nGlobal flags:\n" +
		"  --json        machine-readable output, one JSON document per line\n" +
		"  --quiet       suppress informational output (never suppresses errors)\n" +
		"  --verbose     show additional detail, including error causes\n" +
		"  --no-color    disable ANSI color even on an interactive terminal\n" +
		"  --help, -h    show this help\n")

	return b.String()
}

func summaries(cmds []*Command) []commandSummary {
	out := make([]commandSummary, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, commandSummary{Name: c.Name, Short: c.Short})
	}
	return out
}

func names(cmds []*Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.Name)
	}
	return out
}

// rootHelp builds the helpResult for "nise help" / bare "nise".
func rootHelp(cmds []*Command) helpResult {
	return helpResult{
		Usage:    "nise <command> [flags]",
		Commands: summaries(cmds),
	}
}

// commandHelp builds the helpResult for a resolved command reached via
// path (the sequence of command-name tokens that led to it).
func commandHelp(path []string, cmd *Command) helpResult {
	flags := flagsOf(cmd)

	usage := "nise " + strings.Join(path, " ")
	if len(cmd.Subcommands) > 0 {
		usage += " <subcommand>"
	}
	if cmd.Args != "" {
		usage += " " + cmd.Args
	}
	// "[flags]" is gated on the command actually registering one, not on
	// NewFlagSet being non-nil. `nise generate feature` builds an empty
	// FlagSet (dispatch wants one either way) and used to advertise flags
	// it does not have, while its one real argument went unmentioned.
	if len(flags) > 0 {
		usage += " [flags]"
	}
	return helpResult{
		Path:     path,
		Short:    cmd.Short,
		Usage:    usage,
		Commands: summaries(cmd.Subcommands),
		Flags:    flags,
	}
}

// flagsOf builds cmd's flag listing by constructing its flag.FlagSet (the
// same way dispatch does) and visiting every flag it registers. Building a
// throwaway FlagSet purely to describe it is cheap and keeps this the only
// place that needs to know how flag.FlagSet exposes its flags.
func flagsOf(cmd *Command) []flagSummary {
	if cmd.NewFlagSet == nil {
		return nil
	}
	fs := cmd.NewFlagSet()
	var out []flagSummary
	fs.VisitAll(func(f *flag.Flag) {
		out = append(out, flagSummary{Name: f.Name, Default: f.DefValue, Usage: f.Usage})
	})
	return out
}

// printHelp is the single choke point that writes a helpResult through the
// Writer, keeping "nise help" and "nise <cmd> --help" identically rendered
// regardless of mode.
func printHelp(out output.Writer, h helpResult) {
	out.Result(h)
}
