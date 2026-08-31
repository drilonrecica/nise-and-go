package feature

import (
	"bytes"
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
	"text/template"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/templates"
)

// Options are the inputs a slice is a pure function of.
//
// Everything here is either typed by the person running the command or read
// from the project on disk. Nothing is read from the environment, the clock,
// or the network, which is what lets two runs with equal options produce
// byte-identical output.
type Options struct {
	// Kind is what to generate.
	Kind Kind
	// Name is the canonical singular name, already validated by the CLI.
	Name string
	// ModulePath is the generated project's Go module path, read from its
	// go.mod. The generated imports need it and nothing else does.
	ModulePath string
	// AppName is the application's name, read from the project's directory.
	// The generated pages put it in their titles.
	AppName string
	// NextMigration is the version a resource's migration is written as. It
	// is the number after the highest already in db/migrations, read from the
	// project: the runtime's compatibility check refuses a history with a
	// gap, and a gap is how a missing migration hides. Ignored for a feature,
	// which adds no table.
	NextMigration int
}

// Insertion is one line a person adds to a file they own.
//
// It exists because `nise generate` writes no file it did not create (ADR
// 0026), so everything a slice needs in `api/openapi.yaml`, `internal/app/
// app.go`, the API adapter, or the navigation list is printed instead. Each
// one names the file, says where in it the line goes, and gives the exact
// text.
type Insertion struct {
	// File is the path, relative to the project root, to edit.
	File string
	// Anchor is where in that file the snippet goes, in words a person can
	// find: the name of the function, the block, or the neighbouring entry.
	Anchor string
	// Snippet is the exact text to add.
	Snippet string
}

// Plan is everything one generate command produces.
type Plan struct {
	// Files are the new files to write, sorted by path. None of them exists
	// in a correct run; Write refuses the whole plan if any does.
	Files []generator.File
	// Insertions are the edits the person makes, in the order to apply them.
	Insertions []Insertion
	// Commands are what to run afterwards, in order. They are printed rather
	// than run: generation touches no toolchain, no network, and no database,
	// and a command that quietly ran `sqlc generate` would be a command that
	// quietly rewrote a directory.
	Commands []string
	// Names are the derived spellings, for the command's own output.
	Names Names
}

// templateData is what every feature template is executed against.
//
// It is a struct rather than a map for the same reason the project generator's
// is: a template naming a field this program does not provide fails at
// execution with the field's name, which is the failure that matters — a
// template silently rendering an empty value is how a generated feature ends
// up with a blank package name.
type templateData struct {
	Names
	// ModulePath is the application's Go module path.
	ModulePath string
	// PermissionRead and PermissionManage are the Go identifiers of the two
	// permissions the slice checks. They are derived from the name rather
	// than configurable: two permissions per resource is the smallest set
	// that can express "look, do not touch", and an application that needs a
	// third writes it into its own catalog.
	PermissionRead   string
	PermissionManage string
	// PermissionReadValue and PermissionManageValue are those permissions'
	// wire names, for the catalog entry the command prints.
	PermissionReadValue   string
	PermissionManageValue string
	// Migration is the zero-padded version a resource's migration is written
	// as.
	Migration string
	// AppName is the application's name, for the page titles the generated
	// Svelte writes.
	AppName string
}

// featureTemplates is every template a `generate feature` run renders, mapped
// to the path it is written to. Both are explicit for the reason the project
// manifest is: a suffix-stripping rule is not something a reviewer can read to
// know what a command creates.
var featureTemplates = []templateFile{
	{Template: "internal/features/README.md.tmpl", Output: "internal/features/{{.Singular}}/README.md"},
	{Template: "internal/features/domain.go.tmpl", Output: "internal/features/{{.Singular}}/domain.go"},
	{Template: "internal/features/usecase.go.tmpl", Output: "internal/features/{{.Singular}}/usecase.go"},
	{Template: "internal/features/feature_test.go.tmpl", Output: "internal/features/{{.Singular}}/{{.Singular}}_test.go"},
}

// resourceTemplates are the files a resource adds on top of a feature: the
// table, the hand-written SQL that reads it, the sqlc contract that turns that
// SQL into typed Go, and a use case that does something with all three.
//
// The sqlc output itself is not here. `sqlc generate` writes store/, it is
// tool-owned, and a generator that wrote a plausible-looking copy of somebody
// else's output would be a generator whose files disagree with the tool the
// moment either changes.
var resourceTemplates = []templateFile{
	{Template: "internal/features/README.md.tmpl", Output: "internal/features/{{.Singular}}/README.md"},
	{Template: "internal/features/domain.go.tmpl", Output: "internal/features/{{.Singular}}/domain.go"},
	{Template: "internal/features/resource_usecase.go.tmpl", Output: "internal/features/{{.Singular}}/usecase.go"},
	{Template: "internal/features/resource_test.go.tmpl", Output: "internal/features/{{.Singular}}/{{.Singular}}_test.go"},
	{Template: "internal/features/sqlc.yaml.tmpl", Output: "internal/features/{{.Singular}}/sqlc.yaml"},
	{Template: "internal/features/queries/queries.sql.tmpl", Output: "internal/features/{{.Singular}}/queries/{{.Singular}}.sql"},
	{Template: "db/migrations/resource.sql.tmpl", Output: "db/migrations/{{.Migration}}_{{.Plural}}.sql"},
	{Template: "api/resource.yaml.tmpl", Output: "api/{{.Singular}}.yaml"},
	{Template: "internal/platform/httpapi/resource.go.tmpl", Output: "internal/platform/httpapi/{{.Singular}}.go"},
	{Template: "frontend/lib/features/api.ts.tmpl", Output: "frontend/src/lib/features/{{.Singular}}/api.ts"},
	{Template: "frontend/routes/form.svelte.tmpl", Output: "frontend/src/lib/features/{{.Singular}}/{{.Component}}Form.svelte"},
	{Template: "frontend/routes/list.svelte.tmpl", Output: "frontend/src/routes/(app)/{{.Plural}}/+page.svelte"},
	{Template: "frontend/routes/new.svelte.tmpl", Output: "frontend/src/routes/(app)/{{.Plural}}/new/+page.svelte"},
	{Template: "frontend/routes/detail.svelte.tmpl", Output: "frontend/src/routes/(app)/{{.Plural}}/[id]/+page.svelte"},
	{Template: "frontend/routes/edit.svelte.tmpl", Output: "frontend/src/routes/(app)/{{.Plural}}/[id]/edit/+page.svelte"},
	{Template: "frontend/lib/features/form.browser.test.ts.tmpl", Output: "frontend/src/lib/features/{{.Singular}}/{{.Component}}Form.browser.test.ts"},
	{Template: "internal/features/resource_integration_test.go.tmpl", Output: "internal/features/{{.Singular}}/{{.Singular}}_integration_test.go"},
	{Template: "internal/platform/httpapi/resource_test.go.tmpl", Output: "internal/platform/httpapi/{{.Singular}}_test.go"},
}

// templateFile maps one embedded template to its output path.
type templateFile struct {
	Template string
	Output   string
}

// templateRoot is the directory inside templates.FeatureFS holding them.
const templateRoot = "feature"

// NewPlan renders one slice into memory. Nothing is written.
func NewPlan(opts Options) (Plan, error) {
	names, err := NewNames(opts.Name)
	if err != nil {
		return Plan{}, err
	}
	if opts.ModulePath == "" {
		return Plan{}, fmt.Errorf("feature: the project's module path is required")
	}
	if opts.Kind != KindFeature && opts.Kind != KindResource {
		return Plan{}, fmt.Errorf("feature: unknown kind %q", opts.Kind)
	}

	sources := featureTemplates
	if opts.Kind == KindResource {
		if opts.NextMigration < 1 {
			return Plan{}, fmt.Errorf("feature: a resource needs the next migration version; got %d", opts.NextMigration)
		}
		sources = resourceTemplates
	}

	data := templateData{
		Names:                 names,
		ModulePath:            opts.ModulePath,
		PermissionRead:        names.TitlePlural + "Read",
		PermissionManage:      names.TitlePlural + "Manage",
		PermissionReadValue:   names.Plural + ".read",
		PermissionManageValue: names.Plural + ".manage",
		Migration:             fmt.Sprintf("%05d", opts.NextMigration),
		AppName:               opts.AppName,
	}

	files := make([]generator.File, 0, len(sources))
	for _, tf := range sources {
		source, err := templates.FeatureFS.ReadFile(path.Join(templateRoot, tf.Template))
		if err != nil {
			return Plan{}, fmt.Errorf("reading template %s: %w", tf.Template, err)
		}
		content, err := render(tf.Template, string(source), data)
		if err != nil {
			return Plan{}, err
		}
		outPath, err := renderString(tf.Template+" (output path)", tf.Output, data)
		if err != nil {
			return Plan{}, err
		}
		// Every generated slice is application-owned from the moment it is
		// written; Nise regenerates none of it (ADR 0026).
		files = append(files, generator.File{Path: outPath, Content: content, Owner: generator.OwnerApp})
	}

	slices.SortFunc(files, func(a, b generator.File) int { return cmp.Compare(a.Path, b.Path) })
	for i := 1; i < len(files); i++ {
		if files[i].Path == files[i-1].Path {
			return Plan{}, fmt.Errorf("feature: two templates both write %s", files[i].Path)
		}
	}

	return Plan{
		Files:      files,
		Insertions: insertions(data),
		Commands:   commands(opts.Kind),
		Names:      names,
	}, nil
}

// insertions are the lines the person adds, in the order to apply them.
//
// The order is the order they depend on each other: the permissions exist
// before the use case that checks them is constructed, and the use case exists
// before anything is wired to it.
func insertions(data templateData) []Insertion {
	shared := []Insertion{
		{
			File:   "internal/platform/authorization/catalog.go",
			Anchor: "the permission block, the permissions() list, and the administrator role",
			Snippet: fmt.Sprintf(`// %s permits reading %s.
%s = authz.MustPermission(%q)
// %s permits creating, changing, and removing them.
%s = authz.MustPermission(%q)`,
				data.PermissionRead, data.Plural, data.PermissionRead, data.PermissionReadValue,
				data.PermissionManage, data.PermissionManage, data.PermissionManageValue),
		},
		{
			File:   "internal/app/app.go",
			Anchor: "beside the other use-case constructors, before the API adapter is built",
			Snippet: fmt.Sprintf(`%s, err := %s.New%s(transactor)
if err != nil {
	return fmt.Errorf("building the %s use case: %%w", err)
}`, data.Plural, data.Singular, data.TitlePlural, data.Singular),
		},
		{
			File:    "internal/app/app.go",
			Anchor:  "the import block",
			Snippet: fmt.Sprintf(`"%s/internal/features/%s"`, data.ModulePath, data.Singular),
		},
	}
	if data.Migration == "" {
		return shared
	}
	return append(shared,
		Insertion{
			File:   "api/openapi.yaml",
			Anchor: "paths: and components.schemas:",
			Snippet: fmt.Sprintf(`Merge api/%s.yaml into it: its paths: entries under paths:, and its
components.schemas: entries under components.schemas:. Every $ref in the
fragment is already written to resolve once merged. Delete the fragment
afterwards — openapi.yaml stays the single authoritative document, and a
second file that generated code also read would be a second contract to
keep in step.`, data.Singular),
		},
		Insertion{
			File:   "internal/platform/httpapi/api.go",
			Anchor: "the Server struct, the ServerDeps struct, and NewServer's nil check and assignment",
			Snippet: fmt.Sprintf(`// in Server:
	%s *%s.%s
// in ServerDeps:
	// %s is the %s use case.
	%s *%s.%s
// in NewServer's switch, beside the other required dependencies:
	case deps.%s == nil:
		return nil, errors.New("httpapi: the %s use case is required")
// and in the returned &Server{...}:
	%s: deps.%s,`,
				data.Plural, data.Singular, data.TitlePlural,
				data.TitlePlural, data.Singular,
				data.TitlePlural, data.Singular, data.TitlePlural,
				data.TitlePlural, data.Singular,
				data.Plural, data.TitlePlural),
		},
		Insertion{
			File:    "internal/app/app.go",
			Anchor:  "the httpapi.ServerDeps literal",
			Snippet: fmt.Sprintf(`%s: %s,`, data.TitlePlural, data.Plural),
		},
		Insertion{
			File:   "internal/platform/httpapi/api_test.go",
			Anchor: "testServerDeps, beside the other dependencies",
			Snippet: fmt.Sprintf(`%s: &%s.%s{},

// NewServer refuses a missing dependency, which is what makes a half-wired
// surface a startup failure rather than a nil dereference on the first
// request — so the project's own test helper has to supply this one too.`,
				data.TitlePlural, data.Singular, data.TitlePlural),
		},
	)
}

// commands are what to run after the files are written, in order.
//
// They are printed rather than run. Generation touches no toolchain, no
// network, and no database — that is what makes `nise new` and `nise generate`
// safe to run anywhere — and a command that quietly ran `sqlc generate` would
// be one that quietly rewrote a directory.
func commands(kind Kind) []string {
	if kind != KindResource {
		return nil
	}
	return []string{
		"make sqlc-generate     # write internal/features/*/store from the SQL above",
		"make api-generate      # regenerate the strict bindings from the contract",
		"make api-types-generate # regenerate the frontend's models from the same",
		"make migration-test    # apply the new migration against a disposable database",
	}
}

// render executes one template against data.
//
// Option("missingkey=error") is set for the same reason the project
// generator sets it: with a struct value text/template already fails on a
// field that does not exist, and the option is what makes the same true for
// any map-valued field a template indexes.
func render(name, source string, data templateData) ([]byte, error) {
	raw, err := renderString(name, source, data)
	if err != nil {
		return nil, err
	}
	return normalizeContent([]byte(raw)), nil
}

// renderString executes one template and returns its output verbatim. It is
// what renders an output path, where a trailing newline would become part of
// the filename.
func renderString(name, source string, data templateData) (string, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(source)
	if err != nil {
		return "", fmt.Errorf("parsing template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("rendering template %s: %w", name, err)
	}
	return buf.String(), nil
}

// normalizeContent forces LF line endings and exactly one trailing newline, so
// a checkout with core.autocrlf enabled cannot make one machine's generated
// feature differ from another's.
func normalizeContent(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	b = bytes.TrimRight(b, "\n")
	if len(b) == 0 {
		return []byte("\n")
	}
	return append(b, '\n')
}

// String renders one insertion for the terminal.
func (i Insertion) String() string {
	var out strings.Builder
	out.WriteString(i.File)
	out.WriteString("\n  ")
	out.WriteString(i.Anchor)
	out.WriteString("\n\n")
	for _, line := range strings.Split(i.Snippet, "\n") {
		out.WriteString("    ")
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}
