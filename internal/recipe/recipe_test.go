package recipe_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

const goldenPath = "testdata/recipe.golden.json"

// valid returns a recipe with the given modules and otherwise fixed fields, so
// tests vary one thing at a time.
func valid(modules ...recipe.Module) recipe.Recipe {
	if modules == nil {
		modules = []recipe.Module{}
	}
	return recipe.Recipe{
		SchemaVersion:  recipe.SchemaVersion,
		Profile:        recipe.ProfileGoChiPostgresSvelte,
		Modules:        modules,
		CLIVersion:     "v0.1.0",
		RuntimeVersion: "v0.1.0",
	}
}

// moduleSubsets returns every subset of the canonical module list, each in
// canonical (sorted) order.
func moduleSubsets() [][]recipe.Module {
	all := recipe.Modules()
	subsets := make([][]recipe.Module, 0, 1<<len(all))
	for mask := 0; mask < 1<<len(all); mask++ {
		subset := []recipe.Module{}
		for i, m := range all {
			if mask&(1<<i) != 0 {
				subset = append(subset, m)
			}
		}
		subsets = append(subsets, subset)
	}
	return subsets
}

func TestMarshalMatchesGoldenFile(t *testing.T) {
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	got, err := recipe.Marshal(valid(recipe.Modules()...))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Marshal output does not match %s\ngot:\n%s\nwant:\n%s", goldenPath, got, want)
	}
}

func TestGoldenFileRoundTrips(t *testing.T) {
	data, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	got, err := recipe.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal golden file: %v", err)
	}
	if !equal(got, valid(recipe.Modules()...)) {
		t.Errorf("golden file parsed to %+v", got)
	}
}

func TestRoundTripEveryModuleSubset(t *testing.T) {
	for _, subset := range moduleSubsets() {
		t.Run(name(subset), func(t *testing.T) {
			want := valid(subset...)
			data, err := recipe.Marshal(want)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			got, err := recipe.Unmarshal(data)
			if err != nil {
				t.Fatalf("Unmarshal(%s): %v", data, err)
			}
			if !equal(got, want) {
				t.Errorf("round trip changed the recipe\ngot:  %+v\nwant: %+v", got, want)
			}
		})
	}
}

func TestMarshalIsDeterministic(t *testing.T) {
	all := recipe.Modules()
	reversed := slices.Clone(all)
	slices.Reverse(reversed)

	tests := []struct {
		name string
		in   recipe.Recipe
	}{
		{"canonical order", valid(all...)},
		{"reverse order", valid(reversed...)},
		{"rotated order", valid(all[2], all[3], all[0], all[1])},
		{"empty selection", valid()},
		{"nil selection", recipe.Recipe{
			SchemaVersion:  recipe.SchemaVersion,
			Profile:        recipe.ProfileGoChiPostgresSvelte,
			Modules:        nil,
			CLIVersion:     "v0.1.0",
			RuntimeVersion: "v0.1.0",
		}},
	}

	byName := map[string]string{}
	for _, tc := range tests {
		first, err := recipe.Marshal(tc.in)
		if err != nil {
			t.Fatalf("%s: Marshal: %v", tc.name, err)
		}
		second, err := recipe.Marshal(tc.in)
		if err != nil {
			t.Fatalf("%s: Marshal again: %v", tc.name, err)
		}
		if string(first) != string(second) {
			t.Errorf("%s: two marshals differ\nfirst:\n%s\nsecond:\n%s", tc.name, first, second)
		}
		byName[tc.name] = string(first)
	}

	if byName["canonical order"] != byName["reverse order"] {
		t.Errorf("module order changed the output\n%s\n%s", byName["canonical order"], byName["reverse order"])
	}
	if byName["canonical order"] != byName["rotated order"] {
		t.Errorf("module order changed the output\n%s\n%s", byName["canonical order"], byName["rotated order"])
	}
	if byName["empty selection"] != byName["nil selection"] {
		t.Errorf("nil and empty module slices encode differently\n%s\n%s", byName["empty selection"], byName["nil selection"])
	}
	if !strings.Contains(byName["empty selection"], `"modules": []`) {
		t.Errorf("empty selection did not encode as []\n%s", byName["empty selection"])
	}
}

func TestMarshalEndsWithNewline(t *testing.T) {
	data, err := recipe.Marshal(valid())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("recipe does not end with a closing brace and newline: %q", data)
	}
}

func TestMarshalRejectsInvalidRecipe(t *testing.T) {
	bad := valid()
	bad.Profile = "htmx"
	if _, err := recipe.Marshal(bad); err == nil {
		t.Fatal("Marshal accepted an invalid recipe")
	}
}

func TestUnmarshalRejects(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr any // *recipe.FieldError, *recipe.SchemaVersionError, or nil
	}{
		{
			name:    "unknown field",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0","database":"mysql"}`,
			want:    "database: is not a recipe field",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "unknown module",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":["billing"],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    `modules[0]: unknown module "billing"`,
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "unknown module lists the accepted values",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":["totp","billing"],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    "accepted: notifications, organizations, totp, uploads",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "duplicate module",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":["totp","totp"],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    `modules[1]: duplicate module "totp"`,
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "unknown profile",
			input:   `{"schemaVersion":1,"profile":"go-chi-sqlite-htmx","modules":[],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    `profile: unknown profile "go-chi-sqlite-htmx" (accepted: go-chi-postgres-svelte)`,
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "future schema version",
			input:   `{"schemaVersion":2,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    "recipe declares version 2, but this nise understands 1",
			wantErr: new(*recipe.SchemaVersionError),
		},
		{
			name:    "future schema version with an unknown field",
			input:   `{"schemaVersion":9,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0","adapters":["s3"]}`,
			want:    "upgrade nise",
			wantErr: new(*recipe.SchemaVersionError),
		},
		{
			name:    "schema version zero",
			input:   `{"schemaVersion":0,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    "schemaVersion: must be 1, found 0",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "negative schema version",
			input:   `{"schemaVersion":-1,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    "schemaVersion: must be 1, found -1",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "missing schema version",
			input:   `{"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    "schemaVersion: is required",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "missing profile",
			input:   `{"schemaVersion":1,"modules":[],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    "profile: is required",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "missing modules",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    "modules: is required and must be an array; write [] when no modules are selected",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "null modules",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":null,"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    "modules: is required and must be an array",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "missing cliVersion",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":[],"runtimeVersion":"v0.1.0"}`,
			want:    "cliVersion: is required",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "missing runtimeVersion",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v0.1.0"}`,
			want:    "runtimeVersion: is required",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "empty cliVersion",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"","runtimeVersion":"v0.1.0"}`,
			want:    "cliVersion: is required",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "cliVersion with a space",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v0.1.0 dirty","runtimeVersion":"v0.1.0"}`,
			want:    "cliVersion: must contain only printable ASCII without spaces",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "cliVersion with a control character",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v0.1.0\u0007","runtimeVersion":"v0.1.0"}`,
			want:    "cliVersion: must contain only printable ASCII without spaces",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:  "empty file",
			input: "",
			want:  "recipe is empty",
		},
		{
			name:  "whitespace only",
			input: "  \n\t ",
			want:  "recipe is empty",
		},
		{
			name:  "malformed JSON",
			input: `{"schemaVersion":1,`,
			want:  "recipe is not valid JSON",
		},
		{
			name:  "top-level array",
			input: `[{"schemaVersion":1}]`,
			want:  "recipe must be a JSON object",
		},
		{
			name:  "top-level string",
			input: `"go-chi-postgres-svelte"`,
			want:  "recipe must be a JSON object",
		},
		{
			name:  "top-level null",
			input: `null`,
			want:  "recipe must be a JSON object",
		},
		{
			name:  "content after the object",
			input: `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"} {}`,
			want:  "found content after it",
		},
		{
			name:    "wrong type for schemaVersion",
			input:   `{"schemaVersion":"1","profile":"go-chi-postgres-svelte","modules":[],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    "found JSON string",
			wantErr: new(*recipe.FieldError),
		},
		{
			name:    "wrong type for modules",
			input:   `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":"totp","cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`,
			want:    "found JSON string",
			wantErr: new(*recipe.FieldError),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := recipe.Unmarshal([]byte(tc.input))
			if err == nil {
				t.Fatalf("Unmarshal accepted the input and returned %+v", got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention the problem\ngot:  %v\nwant substring: %s", err, tc.want)
			}
			switch target := tc.wantErr.(type) {
			case **recipe.FieldError:
				if !errors.As(err, target) {
					t.Errorf("error is not a *recipe.FieldError: %v", err)
				}
			case **recipe.SchemaVersionError:
				if !errors.As(err, target) {
					t.Errorf("error is not a *recipe.SchemaVersionError: %v", err)
				}
			}
		})
	}
}

func TestUnmarshalAcceptsUnsortedModulesAndCanonicalizes(t *testing.T) {
	const input = `{"schemaVersion":1,"profile":"go-chi-postgres-svelte","modules":["uploads","notifications"],"cliVersion":"v0.1.0","runtimeVersion":"v0.1.0"}`
	got, err := recipe.Unmarshal([]byte(input))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := []recipe.Module{recipe.ModuleNotifications, recipe.ModuleUploads}
	if !slices.Equal(got.Modules, want) {
		t.Errorf("modules were not canonicalized: got %v, want %v", got.Modules, want)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		in   recipe.Recipe
		want string
	}{
		{"canonical", valid(recipe.Modules()...), ""},
		{"no modules", valid(), ""},
		{"nil modules are an empty selection", recipe.Recipe{
			SchemaVersion:  recipe.SchemaVersion,
			Profile:        recipe.ProfileGoChiPostgresSvelte,
			CLIVersion:     "v0.1.0",
			RuntimeVersion: "v0.1.0",
		}, ""},
		{"development version string", recipe.Recipe{
			SchemaVersion:  recipe.SchemaVersion,
			Profile:        recipe.ProfileGoChiPostgresSvelte,
			Modules:        []recipe.Module{},
			CLIVersion:     "v0.0.0-20260827120000-abcdefabcdef",
			RuntimeVersion: "(devel)",
		}, ""},
		{"future schema version", func() recipe.Recipe {
			r := valid()
			r.SchemaVersion = 2
			return r
		}(), "recipe declares version 2"},
		{"empty profile", func() recipe.Recipe {
			r := valid()
			r.Profile = ""
			return r
		}(), "profile: is required"},
		{"unknown module", valid("billing"), `modules[0]: unknown module "billing"`},
		{"duplicate module", valid(recipe.ModuleTOTP, recipe.ModuleTOTP), `modules[1]: duplicate module "totp"`},
		{"empty module name", valid(""), `modules[0]: unknown module ""`},
		{"missing runtime version", func() recipe.Recipe {
			r := valid()
			r.RuntimeVersion = ""
			return r
		}(), "runtimeVersion: is required"},
		{"oversized version", func() recipe.Recipe {
			r := valid()
			r.CLIVersion = strings.Repeat("v", 65)
			return r
		}(), "cliVersion: must be at most 64 bytes, found 65"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := recipe.Validate(tc.in)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate rejected a valid recipe: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate accepted %+v", tc.in)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want substring %s", err, tc.want)
			}
		})
	}
}

func TestParseModule(t *testing.T) {
	for _, m := range recipe.Modules() {
		got, err := recipe.ParseModule(string(m))
		if err != nil {
			t.Errorf("ParseModule(%q): %v", m, err)
		}
		if got != m {
			t.Errorf("ParseModule(%q) = %q", m, got)
		}
	}

	for _, bad := range []string{"", "TOTP", "billing", "organisations"} {
		if _, err := recipe.ParseModule(bad); err == nil {
			t.Errorf("ParseModule(%q) accepted an unknown module", bad)
		} else if !strings.Contains(err.Error(), "accepted: notifications, organizations, totp, uploads") {
			t.Errorf("ParseModule(%q) error does not list the accepted values: %v", bad, err)
		}
	}
}

func TestParseProfile(t *testing.T) {
	got, err := recipe.ParseProfile(string(recipe.ProfileGoChiPostgresSvelte))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	if got != recipe.ProfileGoChiPostgresSvelte {
		t.Errorf("ParseProfile returned %q", got)
	}
	if _, err := recipe.ParseProfile("go-chi-sqlite-htmx"); err == nil {
		t.Error("ParseProfile accepted an unknown profile")
	}
}

func TestModulesReturnsACopy(t *testing.T) {
	first := recipe.Modules()
	first[0] = "mutated"
	if recipe.Modules()[0] != recipe.ModuleNotifications {
		t.Error("Modules returned a slice that shares state with the package")
	}
}

func TestHasModule(t *testing.T) {
	r := valid(recipe.ModuleTOTP, recipe.ModuleUploads)
	if !r.HasModule(recipe.ModuleTOTP) {
		t.Error("HasModule missed a selected module")
	}
	if r.HasModule(recipe.ModuleOrganizations) {
		t.Error("HasModule reported an unselected module")
	}
}

func TestLoad(t *testing.T) {
	data, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	fsys := fstest.MapFS{
		recipe.FileName:  &fstest.MapFile{Data: data},
		"broken.json":    &fstest.MapFile{Data: []byte(`{"schemaVersion":1}`)},
		"notobject.json": &fstest.MapFile{Data: []byte(`[]`)},
	}

	got, err := recipe.Load(fsys, recipe.FileName)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !equal(got, valid(recipe.Modules()...)) {
		t.Errorf("Load returned %+v", got)
	}

	if _, err := recipe.Load(fsys, "missing.json"); err == nil {
		t.Error("Load accepted a missing file")
	} else if !strings.Contains(err.Error(), "read recipe") {
		t.Errorf("missing-file error is unclear: %v", err)
	}

	if _, err := recipe.Load(fsys, "broken.json"); err == nil {
		t.Error("Load accepted an incomplete recipe")
	} else if !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("error does not name the file: %v", err)
	}

	if _, err := recipe.Load(fsys, "notobject.json"); err == nil {
		t.Error("Load accepted a JSON array")
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, recipe.FileName)

	if err := recipe.Save(path, valid(recipe.Modules()...)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written recipe: %v", err)
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Save wrote:\n%s\nwant:\n%s", got, want)
	}

	// Saving again must not change the bytes.
	if err := recipe.Save(path, valid(recipe.Modules()...)); err != nil {
		t.Fatalf("Save again: %v", err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten recipe: %v", err)
	}
	if string(again) != string(got) {
		t.Error("saving the same recipe twice produced different bytes")
	}

	bad := valid()
	bad.CLIVersion = ""
	if err := recipe.Save(filepath.Join(dir, "invalid.json"), bad); err == nil {
		t.Error("Save wrote an invalid recipe")
	}
	if _, err := os.Stat(filepath.Join(dir, "invalid.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("Save created a file for an invalid recipe")
	}
}

func equal(a, b recipe.Recipe) bool {
	return a.SchemaVersion == b.SchemaVersion &&
		a.Profile == b.Profile &&
		a.CLIVersion == b.CLIVersion &&
		a.RuntimeVersion == b.RuntimeVersion &&
		slices.Equal(a.Modules, b.Modules)
}

func name(modules []recipe.Module) string {
	if len(modules) == 0 {
		return "none"
	}
	parts := make([]string, len(modules))
	for i, m := range modules {
		parts[i] = string(m)
	}
	return strings.Join(parts, "+")
}
