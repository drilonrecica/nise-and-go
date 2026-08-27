package recipe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// fileMode is the permission Save gives a recipe. The recipe holds no secret.
const fileMode fs.FileMode = 0o644

// wire mirrors Recipe with pointer fields so Unmarshal can tell an absent or
// null field from a zero value.
type wire struct {
	SchemaVersion  *int      `json:"schemaVersion"`
	Profile        *string   `json:"profile"`
	Modules        *[]string `json:"modules"`
	CLIVersion     *string   `json:"cliVersion"`
	RuntimeVersion *string   `json:"runtimeVersion"`
}

// Marshal validates r and encodes it as the canonical recipe document: fixed
// field order, two-space indentation, sorted modules, no HTML escaping, and a
// trailing newline. Equal recipes always produce equal bytes, whatever order
// their modules were built in.
func Marshal(r Recipe) ([]byte, error) {
	c, err := canonical(r)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("encode recipe: %w", err)
	}
	return buf.Bytes(), nil
}

// Unmarshal parses and validates a recipe document. It rejects unknown fields,
// a non-object document, content after the object, absent or null required
// fields, unknown or duplicate modules, an unknown profile, and a schema
// version this build does not understand. The returned recipe is canonical:
// its module list is sorted and never nil.
func Unmarshal(data []byte) (Recipe, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Recipe{}, errors.New("recipe is empty")
	}
	if bytes.TrimLeft(data, " \t\r\n")[0] != '{' {
		return Recipe{}, errors.New("recipe must be a JSON object")
	}

	// The schema version is read first, leniently, before any strict check can
	// object to it. A newer recipe is expected to carry fields this build has
	// never heard of, and "upgrade nise" is a more useful answer than "unknown
	// field".
	var probe struct {
		SchemaVersion *int `json:"schemaVersion"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&probe); err != nil {
		return Recipe{}, decodeError(err)
	}
	if probe.SchemaVersion != nil && *probe.SchemaVersion > SchemaVersion {
		return Recipe{}, &SchemaVersionError{Found: *probe.SchemaVersion, Supported: SchemaVersion}
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var w wire
	if err := dec.Decode(&w); err != nil {
		return Recipe{}, decodeError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Recipe{}, errors.New("recipe must contain exactly one JSON object; found content after it")
	}

	r, err := fromWire(w)
	if err != nil {
		return Recipe{}, err
	}
	return canonical(r)
}

// fromWire enforces field presence, which Validate cannot see once the document
// has been turned into values.
func fromWire(w wire) (Recipe, error) {
	if w.SchemaVersion == nil {
		return Recipe{}, &FieldError{
			Field:   "schemaVersion",
			Problem: fmt.Sprintf("is required and must be %d", SchemaVersion),
		}
	}
	if w.Profile == nil {
		return Recipe{}, &FieldError{
			Field:    "profile",
			Problem:  "is required",
			Accepted: profileNames(),
		}
	}
	if w.Modules == nil {
		return Recipe{}, &FieldError{
			Field:   "modules",
			Problem: "is required and must be an array; write [] when no modules are selected",
		}
	}
	if w.CLIVersion == nil {
		return Recipe{}, &FieldError{Field: "cliVersion", Problem: "is required"}
	}
	if w.RuntimeVersion == nil {
		return Recipe{}, &FieldError{Field: "runtimeVersion", Problem: "is required"}
	}

	modules := make([]Module, len(*w.Modules))
	for i, m := range *w.Modules {
		modules[i] = Module(m)
	}
	return Recipe{
		SchemaVersion:  *w.SchemaVersion,
		Profile:        Profile(*w.Profile),
		Modules:        modules,
		CLIVersion:     *w.CLIVersion,
		RuntimeVersion: *w.RuntimeVersion,
	}, nil
}

// unknownFieldPrefix is the text encoding/json uses when DisallowUnknownFields
// rejects a field. There is no typed error for this case, so the prefix is
// matched to recover the field name; the raw error is wrapped when it does not
// match.
const unknownFieldPrefix = "json: unknown field "

// decodeError turns an encoding/json failure into an error that names the
// offending field or offset.
func decodeError(err error) error {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("recipe is not valid JSON at byte offset %d: %w", syntaxErr.Offset, err)
	}

	// A document that stops in the middle of a value surfaces as io.EOF or
	// io.ErrUnexpectedEOF rather than as a *json.SyntaxError.
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return fmt.Errorf("recipe is not valid JSON: the document ends before it is complete: %w", err)
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "recipe"
		}
		return &FieldError{
			Field:   field,
			Problem: fmt.Sprintf("expected %s, found JSON %s", typeErr.Type, typeErr.Value),
		}
	}

	if msg := err.Error(); strings.HasPrefix(msg, unknownFieldPrefix) {
		name := strings.Trim(strings.TrimPrefix(msg, unknownFieldPrefix), `"`)
		return &FieldError{
			Field:    name,
			Problem:  "is not a recipe field",
			Accepted: fieldNames(),
		}
	}

	return fmt.Errorf("recipe is not a valid recipe document: %w", err)
}

// fieldNames lists the recipe's fields, in document order.
func fieldNames() []string {
	return []string{"schemaVersion", "profile", "modules", "cliVersion", "runtimeVersion"}
}

// Load reads and parses the recipe at name within fsys. Errors name the file so
// the caller can report which project failed.
func Load(fsys fs.FS, name string) (Recipe, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Recipe{}, fmt.Errorf("read recipe: %w", err)
	}
	r, err := Unmarshal(data)
	if err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", name, err)
	}
	return r, nil
}

// Save validates r and writes the canonical recipe document to path, replacing
// any existing file. It creates no directories.
func Save(path string, r Recipe) error {
	data, err := Marshal(r)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, fileMode); err != nil {
		return fmt.Errorf("write recipe: %w", err)
	}
	return nil
}
