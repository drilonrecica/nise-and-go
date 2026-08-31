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

	data := templateData{
		Names:                 names,
		ModulePath:            opts.ModulePath,
		PermissionRead:        names.TitlePlural + "Read",
		PermissionManage:      names.TitlePlural + "Manage",
		PermissionReadValue:   names.Plural + ".read",
		PermissionManageValue: names.Plural + ".manage",
	}

	files := make([]generator.File, 0, len(featureTemplates))
	for _, tf := range featureTemplates {
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

	return Plan{Files: files, Insertions: insertions(data), Names: names}, nil
}

// insertions are the lines the person adds, in the order to apply them.
//
// The order is the order they depend on each other: the permissions exist
// before the use case that checks them is constructed, and the use case exists
// before anything is wired to it.
func insertions(data templateData) []Insertion {
	return []Insertion{
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
