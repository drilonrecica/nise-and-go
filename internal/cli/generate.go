package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
)

// Generator is the seam the real M7 generators implement. Nise task
// M1-014 ships no implementation of it at all — see notImplementedGenerator
// below — because the generators themselves are milestone M7's job, not
// this task's. `nise generate`'s own command files resolve everything
// that does not require an actual generator: subcommand structure, help
// text, argument parsing, and name validation. Only the one line that
// constructs the Generator passed to generateCommand needs to change when
// M7 lands; the flag parsing, validation, and error rendering around it do
// not.
type Generator interface {
	// GenerateFeature scaffolds a new vertical-slice feature named name
	// (already validated and canonicalized by ValidateResourceName) into
	// the project at root.
	GenerateFeature(ctx context.Context, root, name string) error
	// GenerateResource scaffolds a full CRUD resource named name into the
	// project at root.
	GenerateResource(ctx context.Context, root, name string) error
}

// notImplementedMilestone names the milestone that adds a real Generator.
// It appears verbatim in the error nise generate returns today, so a user
// (or a script parsing --json output) is told exactly what to wait for,
// not just that something failed.
const notImplementedMilestone = "M7"

// NotImplementedError is returned by every notImplementedGenerator method.
// It is exported so a future M7 Generator's own tests (or this package's)
// can assert a command surfaces it correctly with errors.As, without
// string-matching an error message.
type NotImplementedError struct {
	// Milestone is the milestone that will add a real implementation.
	Milestone string
	// Kind is "feature" or "resource".
	Kind string
}

// Error implements error.
func (e *NotImplementedError) Error() string {
	return fmt.Sprintf("nise generate %s is not implemented in this version (arrives with milestone %s)", e.Kind, e.Milestone)
}

// notImplementedGenerator is the only Generator this build wires in. Its
// methods do no work and touch no filesystem: M1-014's brief is explicit
// that `nise generate` must never fake success, since the M7 generators
// this command delegates to do not exist yet.
type notImplementedGenerator struct{}

func (notImplementedGenerator) GenerateFeature(_ context.Context, _, _ string) error {
	return &NotImplementedError{Milestone: notImplementedMilestone, Kind: "feature"}
}

func (notImplementedGenerator) GenerateResource(_ context.Context, _, _ string) error {
	return &NotImplementedError{Milestone: notImplementedMilestone, Kind: "resource"}
}

// resourceNameRE is the pattern a nise generate feature or resource name
// must match: a single word starting with a letter, containing only
// letters and digits. docs/generated-application-layout.md's naming rules
// require this twice over — "Go directory names contain no underscores or
// hyphens" for the directory this name becomes, and "match their Go
// package name" for the identifier the generated Go code uses — so one
// pattern serves both.
var resourceNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// maxResourceNameLen bounds the name so a pathological input cannot reach
// a filesystem call with an unbounded path component once M7 exists.
const maxResourceNameLen = 64

// reservedResourceNames rejects a name regardless of case, because it
// already means something else in a generated project, in Go itself, or
// on a filesystem this project must run on: Go's own keywords (this name
// becomes a Go package and type name), the three core-service feature
// names ADR 0009 reserves for `nise new` (auth, authz, audit), the
// generic directory names ADR 0009 explicitly excludes from internal/
// (util, common, shared, pkg), the ownership-marking directory names
// docs/generated-application-layout.md defines (store, openapigen,
// embedded) — a feature or resource sharing one of those names would
// collide, confusingly, with the very ownership signal the path is
// supposed to carry — and Windows's reserved device names (con, prn, aux,
// nul, com1-com9, lpt1-lpt9), which docs/generated-application-layout.md's
// naming rules already reason about cross-platform filesystem constraints
// for (the case-collision rule) and which this list now joins: those
// names are reserved by the Windows filesystem itself, regardless of
// extension, so `internal/features/con/` cannot be created there at all —
// a validation rejection here is a far clearer failure than the obscure
// OS error a real M7 generator would otherwise hit. The check is exact,
// case-insensitive whole-name matching only: "console" and "nullable"
// legitimately do not collide with "con"/"nul" merely by starting with
// them.
var reservedResourceNames = map[string]struct{}{
	"break": {}, "case": {}, "chan": {}, "const": {}, "continue": {},
	"default": {}, "defer": {}, "else": {}, "fallthrough": {}, "for": {},
	"func": {}, "go": {}, "goto": {}, "if": {}, "import": {},
	"interface": {}, "map": {}, "package": {}, "range": {}, "return": {},
	"select": {}, "struct": {}, "switch": {}, "type": {}, "var": {},

	"auth": {}, "authz": {}, "audit": {},

	"util": {}, "common": {}, "shared": {}, "pkg": {},

	"store": {}, "openapigen": {}, "embedded": {},

	"con": {}, "prn": {}, "aux": {}, "nul": {},
	"com1": {}, "com2": {}, "com3": {}, "com4": {}, "com5": {},
	"com6": {}, "com7": {}, "com8": {}, "com9": {},
	"lpt1": {}, "lpt2": {}, "lpt3": {}, "lpt4": {}, "lpt5": {},
	"lpt6": {}, "lpt7": {}, "lpt8": {}, "lpt9": {},
}

// ValidateResourceName reports whether name is an acceptable `nise
// generate feature`/`resource` name and, if so, returns its canonical
// lowercase form — the directory and package name a real generator would
// create. A leading-capital input (e.g. "Order") is accepted and
// lowercased rather than rejected: it is the natural spelling of the Go
// exported type name the same generator would eventually write for it,
// and requiring the caller to already know to lowercase it before typing
// it would be a needless trap.
func ValidateResourceName(name string) (string, error) {
	if name == "" {
		return "", errors.New("name must not be empty")
	}
	if len(name) > maxResourceNameLen {
		return "", fmt.Errorf("name %q is longer than the %d-character limit", name, maxResourceNameLen)
	}
	if !resourceNameRE.MatchString(name) {
		return "", fmt.Errorf(
			"name %q must start with a letter and contain only letters and digits — no underscore, hyphen, or space (docs/generated-application-layout.md: \"Go directory names contain no underscores or hyphens\")",
			name,
		)
	}
	lower := strings.ToLower(name)
	if _, reserved := reservedResourceNames[lower]; reserved {
		return "", fmt.Errorf("%q is a reserved name and cannot be used as a feature or resource name", lower)
	}
	return lower, nil
}

// generateCommand implements `nise generate` (Nise tasks M1-014): the
// command surface for the feature and resource generators milestone M7
// will build. See this file's Generator doc comment for the delegation
// seam and why notImplementedGenerator is wired in today.
func generateCommand() *Command {
	return &Command{
		Name:  "generate",
		Short: "Generate a feature or resource in a project (not implemented until milestone M7)",
		Subcommands: []*Command{
			generateFeatureCommand(notImplementedGenerator{}),
			generateResourceCommand(notImplementedGenerator{}),
		},
	}
}

func generateFeatureCommand(gen Generator) *Command {
	return &Command{
		Name:  "feature",
		Short: "Scaffold a new vertical-slice feature (not implemented until milestone M7)",
		NewFlagSet: func() *flag.FlagSet {
			return flag.NewFlagSet("feature", flag.ContinueOnError)
		},
		Run: generateRun("feature", gen.GenerateFeature),
	}
}

func generateResourceCommand(gen Generator) *Command {
	return &Command{
		Name:  "resource",
		Short: "Scaffold a full CRUD resource within a feature (not implemented until milestone M7)",
		NewFlagSet: func() *flag.FlagSet {
			return flag.NewFlagSet("resource", flag.ContinueOnError)
		},
		Run: generateRun("resource", gen.GenerateResource),
	}
}

// generateRun builds the shared Run body for both `generate feature` and
// `generate resource`: exactly one positional argument (the name),
// validated genuinely before any delegation happens, then delegated to
// do, which is always doomed to fail today (see Generator) but need not
// stay that way — do is the only thing M7 replaces.
func generateRun(kind string, do func(ctx context.Context, root, name string) error) func(ctx context.Context, env *Env) error {
	return func(ctx context.Context, env *Env) error {
		if len(env.Args) != 1 {
			return clierr.Usage(
				fmt.Sprintf("nise generate %s requires exactly one argument: the %s name (got %d)", kind, kind, len(env.Args)),
				fmt.Sprintf(`Run "nise generate %s <name>".`, kind),
			).WithDocs("docs/commands/generate.md#naming-rules")
		}

		name, err := ValidateResourceName(env.Args[0])
		if err != nil {
			return clierr.Usage(err.Error(),
				`Choose a name matching ^[A-Za-z][A-Za-z0-9]*$ that is not a reserved word; see "nise generate `+kind+` --help".`,
			).WithCode("generate.invalid_name").WithDocs("docs/commands/generate.md#naming-rules")
		}

		// root is passed through for the real M7 Generator's future use.
		// It deliberately does not require an existing nise.json project
		// here: nothing in this build actually reads or writes anything
		// under it yet (notImplementedGenerator ignores it entirely), so
		// adding that requirement now would only add a failure mode with
		// no corresponding capability behind it. M7's real Generator is
		// expected to validate root itself before doing any work.
		root, err := os.Getwd()
		if err != nil {
			return clierr.Wrap(err, clierr.ExitError,
				"could not determine the current directory",
				"Retry from a directory you have permission to read.",
			)
		}

		if genErr := do(ctx, root, name); genErr != nil {
			return renderGenerateError(kind, name, genErr)
		}

		env.Out.Success(fmt.Sprintf("generated %s %q", kind, name))
		return nil
	}
}

// renderGenerateError turns a Generator error into the command's returned
// *clierr.Error. A *NotImplementedError becomes the honest, milestone-naming
// failure the brief requires — never a faked success — in both human and
// --json mode alike, since both go through the same clierr.Error renderer.
func renderGenerateError(kind, name string, err error) error {
	var nie *NotImplementedError
	if errors.As(err, &nie) {
		return clierr.New(clierr.ExitError,
			nie.Error(),
			fmt.Sprintf("Write %s %q by hand for now, following docs/generated-application-layout.md, or track milestone %s in the project roadmap.", kind, name, nie.Milestone),
		).WithCode("generate.not_implemented").WithDocs("docs/commands/generate.md")
	}
	return clierr.Wrap(err, clierr.ExitError,
		fmt.Sprintf("could not generate %s %q", kind, name),
		"Check filesystem permissions and retry.",
	).WithDocs("docs/commands/generate.md")
}
