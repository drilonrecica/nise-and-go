package generator

import (
	"bytes"
	"cmp"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"text/template"

	"github.com/drilonrecica/nise-and-go/internal/agentsfile"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
	"github.com/drilonrecica/nise-and-go/templates"
)

// File modes are fixed literals, never inherited from the process umask or
// from a template file's own mode on disk: a generated tree must be
// identical on every machine, and a contributor's umask is exactly the kind
// of ambient state that would make it not be.
const (
	// FileMode is the mode every generated file is written with. It is
	// 0o644 because a generated project's source files are ordinary
	// world-readable source, not secrets — .env.example deliberately
	// carries placeholders and no real value.
	FileMode fs.FileMode = 0o644
	// DirMode is the mode every generated directory is created with.
	DirMode fs.FileMode = 0o755
)

// File is one generated file, fully rendered in memory.
type File struct {
	// Path is the file's path relative to the project root, always
	// slash-separated regardless of the host operating system.
	Path string
	// Content is the file's exact bytes, LF-terminated.
	Content []byte
	// Owner is the ownership model the file's header declares.
	Owner Ownership
}

// Options are the inputs generation is a pure function of. Two calls with
// equal Options produce byte-identical output.
type Options struct {
	// Name is the application's name and the directory it is created in.
	Name string
	// ModulePath is the generated go.mod's module path. An empty value
	// defaults to Name.
	ModulePath string
	// Profile is the generation profile. An empty value defaults to the
	// only profile V1 has.
	Profile recipe.Profile
	// Modules are the selected compile-time modules. They are sorted and
	// deduplicated before anything is written.
	Modules []recipe.Module
	// CLIVersion is the version of the nise binary doing the generating.
	// It is required, and is supplied by the caller rather than read here,
	// so a golden test can pin it.
	CLIVersion string
	// RuntimeVersion is the version of runtime/ the project targets. An
	// empty value defaults to CLIVersion.
	RuntimeVersion string
}

// normalize validates opts and fills its defaults, returning the canonical
// form everything downstream reads.
func (o Options) normalize() (Options, error) {
	if err := ValidateName(o.Name); err != nil {
		return Options{}, err
	}

	out := o
	if out.ModulePath == "" {
		out.ModulePath = out.Name
	}
	if err := ValidateModulePath(out.ModulePath); err != nil {
		return Options{}, err
	}
	if out.Profile == "" {
		out.Profile = recipe.ProfileGoChiPostgresSvelte
	}
	if out.RuntimeVersion == "" {
		out.RuntimeVersion = out.CLIVersion
	}

	out.Modules = slices.Clone(o.Modules)
	slices.Sort(out.Modules)
	out.Modules = slices.Compact(out.Modules)

	return out, nil
}

// Recipe builds the project recipe Options describes. It is exported so the
// CLI can report the recipe it is about to write without re-deriving the
// defaults normalize applies.
func (o Options) Recipe() (recipe.Recipe, error) {
	opts, err := o.normalize()
	if err != nil {
		return recipe.Recipe{}, err
	}
	r := recipe.Recipe{
		SchemaVersion:  recipe.SchemaVersion,
		Profile:        opts.Profile,
		Modules:        opts.Modules,
		CLIVersion:     opts.CLIVersion,
		RuntimeVersion: opts.RuntimeVersion,
	}
	if err := recipe.Validate(r); err != nil {
		return recipe.Recipe{}, err
	}
	return r, nil
}

// templateData is what every template is executed against. It is a struct
// rather than a map so that a template naming a field this program does not
// provide fails at execution with the field's name, which is the failure
// mode that matters — a template silently rendering an empty value is how a
// generated project ends up with a blank module path.
type templateData struct {
	AppName        string
	ModulePath     string
	Profile        string
	Modules        []string
	NiseModule     string
	NiseVersion    string
	ChiVersion     string
	GoVersion      string
	GoImageTag     string
	NodeVersion    string
	NodeMajor      string
	PnpmVersion    string
	PackageManager string
	FrontendDeps   []Dependency
}

// newTemplateData builds the template data for already-normalized options.
func newTemplateData(opts Options) templateData {
	modules := make([]string, 0, len(opts.Modules))
	for _, m := range opts.Modules {
		modules = append(modules, string(m))
	}
	return templateData{
		AppName:        opts.Name,
		ModulePath:     opts.ModulePath,
		Profile:        string(opts.Profile),
		Modules:        modules,
		NiseModule:     NiseModulePath,
		NiseVersion:    NiseModuleVersion,
		ChiVersion:     ChiVersion,
		GoVersion:      GoDirective,
		GoImageTag:     GoImageTag,
		NodeVersion:    NodeVersion,
		NodeMajor:      NodeMajor,
		PnpmVersion:    PnpmVersion,
		PackageManager: "pnpm@" + PnpmVersion,
		FrontendDeps:   FrontendDependencies(),
	}
}

// Plan renders the whole project into memory, sorted by path. Nothing is
// written to disk: a template that fails to render fails before Write has
// created a single file, so a failed generation never leaves a half-written
// tree behind.
func Plan(opts Options) ([]File, error) {
	normalized, err := opts.normalize()
	if err != nil {
		return nil, err
	}

	r, err := normalized.Recipe()
	if err != nil {
		return nil, err
	}

	files := make([]File, 0, len(templateFiles)+3)

	// The recipe and the agent files are produced by the packages that own
	// their formats, never re-serialized here: recipe.Marshal is the
	// recipe's canonicalizer, and agentsfile.Generate is what nise agents
	// itself writes, so a project nise new creates and a project nise
	// agents regenerates cannot drift apart.
	recipeBytes, err := recipe.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", recipe.FileName, err)
	}
	files = append(files, File{Path: recipe.FileName, Content: recipeBytes, Owner: OwnerNise})

	agents, err := agentsfile.Generate(r)
	if err != nil {
		return nil, fmt.Errorf("generating %s: %w", agentsfile.AgentsFileName, err)
	}
	files = append(files,
		File{Path: agentsfile.AgentsFileName, Content: agents.AgentsMD, Owner: OwnerNise},
		File{
			Path:    path.Join(agentsfile.ArchitectureDir, agentsfile.ArchitectureFileName),
			Content: agents.Architecture,
			Owner:   OwnerNise,
		},
	)

	data := newTemplateData(normalized)
	for _, tf := range templateFiles {
		source, err := templates.FS.ReadFile(path.Join(templateRoot, tf.Template))
		if err != nil {
			return nil, fmt.Errorf("reading template %s: %w", tf.Template, err)
		}
		content, err := render(tf.Template, string(source), data)
		if err != nil {
			return nil, err
		}
		outPath, err := renderString(tf.Template+" (output path)", tf.Output, data)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: outPath, Content: content, Owner: tf.Owner})
	}

	slices.SortFunc(files, func(a, b File) int { return cmp.Compare(a.Path, b.Path) })
	for i := 1; i < len(files); i++ {
		if files[i].Path == files[i-1].Path {
			return nil, fmt.Errorf("generator: two templates both write %s", files[i].Path)
		}
	}
	return files, nil
}

// render executes one template against data.
//
// Option("missingkey=error") is set on every template. With a struct data
// value text/template already fails on a field that does not exist; the
// option is what makes the same true for any map-valued field a template
// indexes, so the rule holds however the data shape grows.
func render(name, source string, data templateData) ([]byte, error) {
	raw, err := renderString(name, source, data)
	if err != nil {
		return nil, err
	}
	return normalizeContent([]byte(raw)), nil
}

// renderString executes one template and returns its output verbatim. It is
// what renders an output path, where a trailing newline would become part
// of the filename.
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

// normalizeContent forces LF line endings and exactly one trailing newline.
//
// The line-ending pass is not defensive noise: a contributor cloning this
// repository on Windows with core.autocrlf enabled gets CRLF template
// files, and without this every generated project on that machine would
// differ byte-for-byte from every other machine's.
func normalizeContent(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	b = bytes.TrimRight(b, "\n")
	if len(b) == 0 {
		return []byte("\n")
	}
	return append(b, '\n')
}
