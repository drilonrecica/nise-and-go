package generator

import (
	"strings"
	"testing"
)

// TestRenderFailsOnAMissingKey covers the rule that a template naming
// something the generator does not provide must fail loudly. The failure
// mode this prevents is the quiet one: text/template's default is to render
// an unknown map key as "<no value>", which would ship a generated project
// with a blank module path or a blank version and no error anywhere.
func TestRenderFailsOnAMissingKey(t *testing.T) {
	t.Parallel()

	data := newTemplateData(Options{Name: "myapp", ModulePath: "example.com/myapp"})

	for _, tc := range []struct {
		label  string
		source string
	}{
		{"unknown field", `module {{.NoSuchField}}`},
		{"unknown nested field", `{{range .FrontendDeps}}{{.NoSuchField}}{{end}}`},
		{"unknown method", `{{.NoSuchMethod 1}}`},
	} {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			out, err := render("probe.tmpl", tc.source, data)
			if err == nil {
				t.Fatalf("render(%q) = %q, nil; want an error", tc.source, out)
			}
			if !strings.Contains(err.Error(), "probe.tmpl") {
				t.Errorf("error %q does not name the failing template", err)
			}
		})
	}
}

// TestRenderNormalizesLineEndings pins the CRLF conversion that keeps
// generation byte-identical on a Windows checkout of this repository.
func TestRenderNormalizesLineEndings(t *testing.T) {
	t.Parallel()

	data := newTemplateData(Options{Name: "myapp", ModulePath: "example.com/myapp"})
	got, err := render("crlf.tmpl", "first\r\nsecond\r\n\r\n\r\n", data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if string(got) != "first\nsecond\n" {
		t.Errorf("render = %q, want %q", got, "first\nsecond\n")
	}
}
