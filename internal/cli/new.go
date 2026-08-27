package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/cli/output"
	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
	"github.com/drilonrecica/nise-and-go/internal/version"
)

// moduleFlag collects a repeatable --module flag. Each value is parsed the
// moment it is given, so `nise new app --module typo` fails at flag parsing
// with the accepted list, rather than after a name prompt or halfway
// through generation.
type moduleFlag struct {
	values []recipe.Module
}

// String implements flag.Value.
func (m *moduleFlag) String() string {
	names := make([]string, 0, len(m.values))
	for _, v := range m.values {
		names = append(names, string(v))
	}
	return strings.Join(names, ",")
}

// Set implements flag.Value.
func (m *moduleFlag) Set(raw string) error {
	module, err := recipe.ParseModule(raw)
	if err != nil {
		return err
	}
	m.values = append(m.values, module)
	return nil
}

// newResult is `nise new`'s primary output in both modes. Every field is
// plain data derived from what was generated: no timing, no host detail,
// nothing a second identical run would report differently.
type newResult struct {
	generator.Result
}

// Human renders the result for a terminal.
func (r newResult) Human() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Created %s — %d files\n\n", r.Root, len(r.Files))
	fmt.Fprintf(&b, "  profile      %s\n", r.Profile)
	fmt.Fprintf(&b, "  module path  %s\n", r.ModulePath)
	if len(r.Modules) == 0 {
		fmt.Fprintf(&b, "  modules      none\n")
	} else {
		fmt.Fprintf(&b, "  modules      %s\n", strings.Join(r.Modules, ", "))
	}
	b.WriteString("\nNext:\n")
	for _, cmd := range r.NextCommands {
		fmt.Fprintf(&b, "  %s\n", cmd)
	}
	b.WriteString("\nnise ran none of these. Generation writes files and nothing else:\n")
	b.WriteString("no network access, no go, no pnpm, no git.")
	return b.String()
}

// newFlags holds every flag `nise new` accepts. It is a struct with a bind
// method rather than a set of closure variables because the flag set is
// built more than once per invocation — see runNew's argument permutation —
// and each rebind must preserve what an earlier parse already collected.
type newFlags struct {
	profile    string
	modulePath string
	modules    moduleFlag
	assumeYes  bool
	noInput    bool
}

// bind declares this command's flags on fs. Every default is the field's
// current value, not a zero literal, so binding the same struct to a second
// flag.FlagSet never discards what the first one parsed.
func (f *newFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&f.profile, "profile", f.profile,
		fmt.Sprintf("generation profile (accepted: %s; default: %s)",
			strings.Join(profileNames(), ", "), recipe.ProfileGoChiPostgresSvelte))
	fs.StringVar(&f.modulePath, "module-path", f.modulePath,
		"Go module path for the generated go.mod (default: the project name)")
	fs.Var(&f.modules, "module",
		fmt.Sprintf("enable a compile-time module; repeatable (accepted: %s)",
			strings.Join(moduleNames(), ", ")))
	fs.BoolVar(&f.assumeYes, "yes", f.assumeYes,
		"accept every default and never prompt")
	fs.BoolVar(&f.noInput, "no-input", f.noInput,
		"never prompt; fail instead of asking (alias of --yes for scripts)")
}

// newCommand implements `nise new` (Nise task M1-008).
func newCommand() *Command {
	flags := &newFlags{}
	return &Command{
		Name:  "new",
		Short: "Create a new Nise application",
		NewFlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("new", flag.ContinueOnError)
			flags.bind(fs)
			return fs
		},
		Run: func(_ context.Context, env *Env) error {
			return runNew(env, flags)
		},
	}
}

// permuteFlags finishes parsing args that the dispatcher's single
// flag.FlagSet.Parse could not reach.
//
// Go's flag package stops at the first non-flag argument, so
// `nise new myapp --yes` leaves the dispatcher with ["myapp", "--yes"] and
// assumeYes still false. That is the ordering a user actually types for a
// command whose one positional argument is its subject, and silently
// ignoring the flag after it would be the worst of the three possible
// behaviors. permuteFlags walks what is left, parsing every further run of
// flags into the same newFlags, and returns the positional arguments in
// order.
func permuteFlags(args []string, flags *newFlags) ([]string, error) {
	var positional []string
	rest := args
	for len(rest) > 0 {
		if rest[0] == "-" || !strings.HasPrefix(rest[0], "-") {
			positional = append(positional, rest[0])
			rest = rest[1:]
			continue
		}
		fs := flag.NewFlagSet("new", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.Usage = func() {}
		flags.bind(fs)
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
	}
	return positional, nil
}

// runNew is `nise new`'s body.
func runNew(env *Env, opts *newFlags) error {
	args, err := permuteFlags(env.Args, opts)
	if err != nil {
		return clierr.Usage(
			fmt.Sprintf("invalid flags for \"nise new\": %s", err),
			`Run "nise new --help" for usage.`,
		).WithDocs("docs/commands/new.md#flags")
	}
	if len(args) > 1 {
		return clierr.Usage(
			fmt.Sprintf("nise new takes at most one argument: the project name (got %d: %s)", len(args), strings.Join(args, " ")),
			`Run "nise new <name>".`,
		).WithDocs("docs/commands/new.md")
	}

	// Prompting is allowed only when there is genuinely a human at the
	// other end: a real terminal, human output mode, and no flag asking
	// for silence. --json implies non-interactive because the Writer is
	// in JSON mode, so a prompt would corrupt the document stream.
	interactive := env.Out.Mode() == output.ModeHuman &&
		env.Caps.StdoutIsTerminal &&
		!env.Caps.CI &&
		!opts.assumeYes &&
		!opts.noInput

	name := ""
	if len(args) == 1 {
		name = args[0]
	}

	prompter := newPrompter(env, interactive)

	if name == "" {
		if !interactive {
			return clierr.Usage(
				"nise new requires the project name when it cannot prompt",
				`Run "nise new <name>". Prompts appear only on an interactive terminal, and never with --json, --yes, or --no-input.`,
			).WithCode("new.name_required").WithDocs("docs/commands/new.md")
		}
		answer, err := prompter.ask("Project name", "")
		if err != nil {
			return err
		}
		name = answer
	}

	if err := generator.ValidateName(name); err != nil {
		return renderNewError(err)
	}

	modulePath := opts.modulePath
	if modulePath == "" {
		answer, err := prompter.ask("Go module path", name)
		if err != nil {
			return err
		}
		modulePath = answer
	}
	if err := generator.ValidateModulePath(modulePath); err != nil {
		return renderNewError(err)
	}

	selectedProfile := recipe.ProfileGoChiPostgresSvelte
	if opts.profile != "" {
		parsed, err := recipe.ParseProfile(opts.profile)
		if err != nil {
			return clierr.Usage(err.Error(),
				fmt.Sprintf("Pass --profile with one of: %s.", strings.Join(profileNames(), ", ")),
			).WithCode("new.invalid_profile").WithDocs("docs/commands/new.md#flags")
		}
		selectedProfile = parsed
	}

	selectedModules := opts.modules.values
	if len(selectedModules) == 0 && interactive {
		chosen, err := prompter.askModules()
		if err != nil {
			return err
		}
		selectedModules = chosen
	}

	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		return clierr.Wrap(cwdErr, clierr.ExitError,
			"could not determine the current directory",
			"Retry from a directory you have permission to read.",
		)
	}
	target := filepath.Join(cwd, name)

	cliVersion := version.Get().Version
	if cliVersion == "" {
		return clierr.New(clierr.ExitError,
			"this nise binary reports no version, so it cannot stamp the project recipe",
			"Reinstall nise from a release build or from `go install`.",
		).WithCode("new.no_version")
	}

	result, writeErr := generator.Write(target, generator.Options{
		Name:           name,
		ModulePath:     modulePath,
		Profile:        selectedProfile,
		Modules:        selectedModules,
		CLIVersion:     cliVersion,
		RuntimeVersion: cliVersion,
	})
	if writeErr != nil {
		return renderNewError(writeErr)
	}

	// The reported root is the name the user typed, not the absolute path
	// generation used: an absolute path is machine-specific, and --json
	// output should not carry one.
	result.Root = name
	env.Out.Result(newResult{*result})
	return nil
}

// renderNewError turns a generator error into the command's returned
// *clierr.Error, so every failure reaches the user as a cause and a
// recovery rather than a raw Go error.
func renderNewError(err error) error {
	var nameErr *generator.NameError
	if errors.As(err, &nameErr) {
		return clierr.Wrap(err, clierr.ExitUsage, nameErr.Error(), nameErr.Action).
			WithCode("new.invalid_name").
			WithDocs("docs/commands/new.md#name-rules")
	}

	var notEmpty *generator.TargetNotEmptyError
	if errors.As(err, &notEmpty) {
		return clierr.Wrap(err, clierr.ExitPrecondition, notEmpty.Error(),
			"Choose a different name, or remove that directory yourself first. nise new has no --force: it never overwrites a directory it did not create.",
		).WithCode("new.target_not_empty").WithDocs("docs/commands/new.md#refusing-to-destroy")
	}

	var notDir *generator.TargetNotDirectoryError
	if errors.As(err, &notDir) {
		return clierr.Wrap(err, clierr.ExitPrecondition, notDir.Error(),
			"Choose a different name, or move that file out of the way yourself first.",
		).WithCode("new.target_not_directory").WithDocs("docs/commands/new.md#refusing-to-destroy")
	}

	return clierr.Wrap(err, clierr.ExitError,
		fmt.Sprintf("could not create the project: %v", err),
		"Check filesystem permissions and retry.",
	).WithDocs("docs/commands/new.md")
}

// prompter asks the questions `nise new` needs when there is a terminal to
// ask them on. When there is not, every ask returns its default
// immediately: a non-interactive run must never block waiting for input
// that is not coming.
type prompter struct {
	out         output.Writer
	in          *bufio.Reader
	interactive bool
}

// newPrompter builds a prompter for env.
func newPrompter(env *Env, interactive bool) *prompter {
	var in *bufio.Reader
	if env.Stdin != nil {
		in = bufio.NewReader(env.Stdin)
	}
	return &prompter{out: env.Out, in: in, interactive: interactive && in != nil}
}

// ask prompts for a single value, returning fallback when the answer is
// empty or when prompting is not allowed.
func (p *prompter) ask(question, fallback string) (string, error) {
	if !p.interactive {
		return fallback, nil
	}
	if fallback == "" {
		p.out.Line(question + ":")
	} else {
		p.out.Line(fmt.Sprintf("%s [%s]:", question, fallback))
	}
	line, err := p.readLine()
	if err != nil {
		return "", err
	}
	if line == "" {
		return fallback, nil
	}
	return line, nil
}

// askModules asks about each compile-time module in turn. The default is
// always no: a module a user did not ask for contributes code, tables, and
// screens they did not ask for either.
func (p *prompter) askModules() ([]recipe.Module, error) {
	if !p.interactive {
		return nil, nil
	}
	p.out.Line("Compile-time modules (unselected modules contribute no code at all):")

	var chosen []recipe.Module
	for _, module := range recipe.Modules() {
		p.out.Line(fmt.Sprintf("  enable %s? [y/N]:", module))
		line, err := p.readLine()
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(line) {
		case "y", "yes":
			chosen = append(chosen, module)
		default:
		}
	}
	return chosen, nil
}

// readLine reads one answer, treating a closed stdin as an empty answer so
// that a piped-but-interactive-looking session ends at its defaults rather
// than failing.
func (p *prompter) readLine() (string, error) {
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
			return "", nil
		}
		return "", clierr.Wrap(err, clierr.ExitError,
			"could not read your answer from standard input",
			`Rerun with the flags instead: nise new <name> --module-path <path> --yes.`,
		)
	}
	return strings.TrimSpace(line), nil
}

// profileNames lists the accepted --profile values.
func profileNames() []string {
	names := make([]string, 0, len(recipe.Profiles()))
	for _, p := range recipe.Profiles() {
		names = append(names, string(p))
	}
	return names
}

// moduleNames lists the accepted --module values.
func moduleNames() []string {
	names := make([]string, 0, len(recipe.Modules()))
	for _, m := range recipe.Modules() {
		names = append(names, string(m))
	}
	return names
}
