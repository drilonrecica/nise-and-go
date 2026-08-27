// Package recipe reads, writes, and validates the Nise project recipe.
//
// The recipe is the small JSON file nise new writes into the root of a
// generated project. It records what was selected — the profile, the
// compile-time modules, and the CLI and runtime versions that produced the
// project — and nothing else. It is not application configuration: runtime
// configuration is typed environment variables. The format is fixed by
// docs/adr/0010-project-recipe-format.md.
//
// Marshal is the canonicalizer. It validates before it encodes, sorts the
// module list, and produces byte-identical output for equal recipes, so a
// caller can compare a committed recipe with Marshal of its own parsed contents
// to detect a hand edit. Unmarshal is strict: unknown fields, unknown modules,
// duplicate modules, unknown profiles, unknown schema versions, missing fields,
// and trailing content are all errors that name the offending field.
//
// This package is private to Nise and imports only the standard library.
package recipe

import (
	"fmt"
	"slices"
	"strings"
)

// SchemaVersion is the recipe schema version this package reads and writes. A
// recipe declaring a higher version is rejected rather than misread.
const SchemaVersion = 1

// FileName is the recipe's name at the root of a generated project. Commands
// find the project root by walking up from the working directory to the first
// ancestor containing this file.
const FileName = "nise.json"

// Profile names the generation profile a project was created from.
type Profile string

// ProfileGoChiPostgresSvelte is the only profile V1 supports: Go, chi,
// PostgreSQL with pgx, sqlc, and Goose, and a statically built SvelteKit
// frontend embedded in the binary.
const ProfileGoChiPostgresSvelte Profile = "go-chi-postgres-svelte"

// Profiles returns every accepted profile, in a stable order.
func Profiles() []Profile {
	return []Profile{ProfileGoChiPostgresSvelte}
}

// ParseProfile converts s to a Profile, reporting a *FieldError that lists the
// accepted values when s is not one of them.
func ParseProfile(s string) (Profile, error) {
	p := Profile(s)
	if !slices.Contains(Profiles(), p) {
		return "", &FieldError{
			Field:    "profile",
			Problem:  fmt.Sprintf("unknown profile %q", s),
			Accepted: profileNames(),
		}
	}
	return p, nil
}

// Module names one compile-time optional module. The set is closed: a module
// that is not listed here does not exist, and a recipe naming one is rejected.
type Module string

// The compile-time modules a project may select.
const (
	// ModuleNotifications adds persistent in-app notifications with optional
	// server-sent-event delivery and a polling fallback.
	ModuleNotifications Module = "notifications"
	// ModuleOrganizations adds multitenancy: org_id ownership and PostgreSQL
	// row-level security.
	ModuleOrganizations Module = "organizations"
	// ModuleTOTP adds time-based one-time passwords and recovery codes.
	ModuleTOTP Module = "totp"
	// ModuleUploads adds file uploads and storage with a staged, validated
	// finalization step.
	ModuleUploads Module = "uploads"
)

// Modules returns every accepted module, sorted. The returned slice is a copy;
// callers may modify it.
func Modules() []Module {
	return []Module{
		ModuleNotifications,
		ModuleOrganizations,
		ModuleTOTP,
		ModuleUploads,
	}
}

// ParseModule converts s to a Module, reporting a *FieldError that lists the
// accepted values when s is not one of them.
func ParseModule(s string) (Module, error) {
	m := Module(s)
	if !slices.Contains(Modules(), m) {
		return "", &FieldError{
			Field:    "modules",
			Problem:  fmt.Sprintf("unknown module %q", s),
			Accepted: moduleNames(),
		}
	}
	return m, nil
}

// Recipe is the parsed content of a project recipe. The struct field order
// fixes the field order of the encoded document.
type Recipe struct {
	// SchemaVersion is the recipe schema version. It must equal SchemaVersion.
	SchemaVersion int `json:"schemaVersion"`
	// Profile is the generation profile the project was created from.
	Profile Profile `json:"profile"`
	// Modules holds the selected compile-time modules. It is sorted and free of
	// duplicates once canonicalized. A nil slice and an empty slice both mean
	// no modules were selected and both encode as [].
	Modules []Module `json:"modules"`
	// CLIVersion is the version of the nise binary that created or last
	// upgraded the project.
	CLIVersion string `json:"cliVersion"`
	// RuntimeVersion is the version of the Nise runtime packages the project
	// targets. It is separate from CLIVersion because an application may pin an
	// older runtime in go.mod than the CLI that generated it.
	RuntimeVersion string `json:"runtimeVersion"`
}

// HasModule reports whether m is selected.
func (r Recipe) HasModule(m Module) bool {
	return slices.Contains(r.Modules, m)
}

// maxVersionLen bounds the version strings so a malformed recipe cannot carry
// an unbounded value into CLI output.
const maxVersionLen = 64

// Validate reports the first rule the recipe breaks, as a *FieldError or a
// *SchemaVersionError. It checks values, not wire representation: a nil module
// slice is treated as an empty selection, because Unmarshal has already
// rejected a recipe whose modules field was absent or null.
func Validate(r Recipe) error {
	if r.SchemaVersion > SchemaVersion {
		return &SchemaVersionError{Found: r.SchemaVersion, Supported: SchemaVersion}
	}
	if r.SchemaVersion < 1 {
		return &FieldError{
			Field:   "schemaVersion",
			Problem: fmt.Sprintf("must be %d, found %d", SchemaVersion, r.SchemaVersion),
		}
	}
	if r.Profile == "" {
		return &FieldError{
			Field:    "profile",
			Problem:  "is required",
			Accepted: profileNames(),
		}
	}
	if !slices.Contains(Profiles(), r.Profile) {
		return &FieldError{
			Field:    "profile",
			Problem:  fmt.Sprintf("unknown profile %q", string(r.Profile)),
			Accepted: profileNames(),
		}
	}
	known := Modules()
	for i, m := range r.Modules {
		if !slices.Contains(known, m) {
			return &FieldError{
				Field:    fmt.Sprintf("modules[%d]", i),
				Problem:  fmt.Sprintf("unknown module %q", string(m)),
				Accepted: moduleNames(),
			}
		}
		if slices.Contains(r.Modules[:i], m) {
			return &FieldError{
				Field:   fmt.Sprintf("modules[%d]", i),
				Problem: fmt.Sprintf("duplicate module %q", string(m)),
			}
		}
	}
	if err := validateVersion("cliVersion", r.CLIVersion); err != nil {
		return err
	}
	return validateVersion("runtimeVersion", r.RuntimeVersion)
}

// validateVersion enforces a non-empty, bounded, printable, space-free version
// string. It deliberately does not require semantic versioning: development
// builds of the CLI carry versions that no released binary ever will, and a
// recipe must still be writable from one.
func validateVersion(field, value string) error {
	if value == "" {
		return &FieldError{Field: field, Problem: "is required"}
	}
	if len(value) > maxVersionLen {
		return &FieldError{
			Field:   field,
			Problem: fmt.Sprintf("must be at most %d bytes, found %d", maxVersionLen, len(value)),
		}
	}
	for _, r := range value {
		if r < '!' || r > '~' {
			return &FieldError{
				Field:   field,
				Problem: fmt.Sprintf("must contain only printable ASCII without spaces, found %q", value),
			}
		}
	}
	return nil
}

// canonical validates r and returns the form Marshal encodes: modules sorted
// and never nil.
func canonical(r Recipe) (Recipe, error) {
	if err := Validate(r); err != nil {
		return Recipe{}, err
	}
	c := r
	c.Modules = make([]Module, len(r.Modules))
	copy(c.Modules, r.Modules)
	slices.Sort(c.Modules)
	return c, nil
}

func profileNames() []string {
	names := make([]string, 0, len(Profiles()))
	for _, p := range Profiles() {
		names = append(names, string(p))
	}
	return names
}

func moduleNames() []string {
	names := make([]string, 0, len(Modules()))
	for _, m := range Modules() {
		names = append(names, string(m))
	}
	return names
}

// joinAccepted renders an accepted-value list for an error message.
func joinAccepted(values []string) string {
	return strings.Join(values, ", ")
}
