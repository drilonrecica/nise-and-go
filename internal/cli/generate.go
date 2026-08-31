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
	"github.com/drilonrecica/nise-and-go/internal/generator/feature"
)

// Generator is the seam the feature generators implement.
//
// It stayed a seam through M1-014, when `nise generate` shipped its whole
// command surface — subcommands, help text, argument parsing, name validation
// — with nothing behind it. M7 replaced the one line that constructs it and
// changed nothing else, which is what the seam was for.
type Generator interface {
	// GenerateFeature scaffolds a new vertical-slice feature named name
	// (already validated and canonicalized by ValidateResourceName) into
	// the project at root.
	GenerateFeature(ctx context.Context, root, name string) (feature.Plan, error)
	// GenerateResource scaffolds a full CRUD resource named name into the
	// project at root.
	GenerateResource(ctx context.Context, root, name string) (feature.Plan, error)
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

// generateCommand implements `nise generate`: the feature and resource
// generators, behind the command surface M1-014 built for them.
func generateCommand() *Command {
	gen := featureGenerator{}
	return &Command{
		Name:  "generate",
		Short: "Generate a feature or resource in a project",
		Subcommands: []*Command{
			generateFeatureCommand(gen),
			generateResourceCommand(gen),
		},
	}
}

func generateFeatureCommand(gen Generator) *Command {
	return &Command{
		Name:  "feature",
		Short: "Scaffold a new vertical-slice feature",
		Args:  "<name>",
		NewFlagSet: func() *flag.FlagSet {
			return flag.NewFlagSet("feature", flag.ContinueOnError)
		},
		Run: generateRun("feature", gen.GenerateFeature),
	}
}

func generateResourceCommand(gen Generator) *Command {
	return &Command{
		Name:  "resource",
		Short: "Scaffold a full CRUD resource within a feature",
		Args:  "<name>",
		NewFlagSet: func() *flag.FlagSet {
			return flag.NewFlagSet("resource", flag.ContinueOnError)
		},
		Run: generateRun("resource", gen.GenerateResource),
	}
}

// generateRun builds the shared Run body for both subcommands: exactly one
// positional argument, validated before any delegation happens, then delegated
// to do.
func generateRun(kind string, do func(ctx context.Context, root, name string) (feature.Plan, error)) func(ctx context.Context, env *Env) error {
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
				`Choose a name matching ^[A-Za-z][A-Za-z0-9]*$ that is not a reserved word.`,
			).WithCode("generate.invalid_name").WithDocs("docs/commands/generate.md#naming-rules")
		}

		root, err := os.Getwd()
		if err != nil {
			return clierr.Wrap(err, clierr.ExitError,
				"could not determine the current directory",
				"Retry from a directory you have permission to read.",
			)
		}

		plan, genErr := do(ctx, root, name)
		if genErr != nil {
			return renderGenerateError(kind, name, genErr)
		}

		env.Out.Result(generateResult{Kind: kind, Name: name, Plan: plan})
		return nil
	}
}

// renderGenerateError turns a Generator failure into the command's own error.
//
// A collision is the one failure with a recovery that is not "try again": the
// feature is already there, and the command refused rather than writing over
// it, so the answer is to pick another name or remove what is there.
func renderGenerateError(kind, name string, err error) error {
	if errors.Is(err, feature.ErrExists) {
		var existing *feature.ExistsError
		paths := ""
		if errors.As(err, &existing) {
			paths = strings.Join(existing.Paths, "\n  ")
		}
		return clierr.New(clierr.ExitError,
			fmt.Sprintf("%s %q already exists; nothing was written", kind, name),
			fmt.Sprintf("These paths are already there:\n  %s\n\nChoose another name, or remove them if they are no longer wanted. A generated slice is yours from the moment it is written, so nise will not replace one.", paths),
		).WithCode("generate.exists").WithDocs("docs/commands/generate.md")
	}
	if errors.Is(err, feature.ErrNotAProject) {
		return clierr.Wrap(err, clierr.ExitError,
			"this directory is not the root of a Go project: no readable go.mod",
			"Run this from the root of a generated application — the directory holding go.mod.",
		).WithCode("generate.not_a_project").WithDocs("docs/commands/generate.md")
	}
	return clierr.Wrap(err, clierr.ExitError,
		fmt.Sprintf("could not generate %s %q", kind, name),
		"Check that you can write to this project, then retry.",
	).WithDocs("docs/commands/generate.md")
}
