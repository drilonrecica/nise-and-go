package generator

import (
	"os"
	"regexp"
	"testing"
)

// The generated go.mod's require block and GoDependencies must name the same
// modules at the same versions.
//
// A dependency nise pins and never bumps is worse than one it does not pin at
// all: the project keeps an old version through every upgrade and nothing says
// so. This test is what makes adding a requirement to the template a change
// that also has to be declared as upgradable.
func TestGoModTemplatePinsExactlyTheDeclaredDependencies(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../templates/new/go.mod.tmpl")
	if err != nil {
		t.Fatalf("reading the go.mod template: %v", err)
	}

	// A require line is "<module> <{{.Version}}>", optionally followed by an
	// // indirect comment. The module directive and the tool block have a
	// different shape and are not matched.
	line := regexp.MustCompile(`(?m)^\t(\S+) \{\{\.(\w+)\}\}(?: // indirect)?$`)
	matches := line.FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		t.Fatal("no require lines were found in the go.mod template, so this test proves nothing")
	}

	declared := make(map[string]string, len(goDependencies))
	for _, dep := range goDependencies {
		declared[dep.Name] = dep.Version
	}

	data := newTemplateData(Options{Name: "app", ModulePath: "example.com/app", CLIVersion: "v0.0.0"})
	rendered := renderedVersions(t, data)

	seen := map[string]bool{}
	for _, match := range matches {
		module, field := match[1], match[2]
		// The framework's own line renders its module path from a field too.
		if module == "{{.NiseModule}}" {
			module = NiseModulePath
		}
		seen[module] = true

		version, ok := declared[module]
		if !ok {
			t.Errorf("go.mod requires %s and GoDependencies does not name it, so `nise upgrade` would never bump it", module)
			continue
		}
		if want := rendered[field]; want != "" && version != want {
			t.Errorf("go.mod pins %s at %s (from %s) and GoDependencies says %s", module, want, field, version)
		}
	}
	for module := range declared {
		if !seen[module] {
			t.Errorf("GoDependencies names %s and the go.mod template does not require it", module)
		}
	}
}

// renderedVersions maps every string field of templateData to its value, so
// the test can resolve a template placeholder name to the version it renders.
func renderedVersions(t *testing.T, data templateData) map[string]string {
	t.Helper()
	return map[string]string{
		"NiseVersion":        data.NiseVersion,
		"ChiVersion":         data.ChiVersion,
		"PgxVersion":         data.PgxVersion,
		"RiverVersion":       data.RiverVersion,
		"SQLCVersion":        data.SQLCVersion,
		"GooseVersion":       data.GooseVersion,
		"OAPICodegenVersion": data.OAPICodegenVersion,
		"OAPIRuntimeVersion": data.OAPIRuntimeVersion,
		"XCryptoVersion":     data.XCryptoVersion,
	}
}
