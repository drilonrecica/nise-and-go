package generator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestValidateNameAccepts(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"a",
		"myapp",
		"my-app",
		"my-app-2",
		"invoices2",
		"a1",
		strings.Repeat("a", generator.MaxNameLen),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := generator.ValidateName(name); err != nil {
				t.Fatalf("ValidateName(%q) = %v, want nil", name, err)
			}
		})
	}
}

func TestValidateNameRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		label   string
		name    string
		problem string
	}{
		{"empty", "", "is empty"},
		{"too long", strings.Repeat("a", generator.MaxNameLen+1), "longer than"},
		{"nul byte", "my\x00app", "control or non-ASCII"},
		{"newline", "my\napp", "control or non-ASCII"},
		{"carriage return", "my\rapp", "control or non-ASCII"},
		{"ansi escape", "my\x1b[31mapp", "control or non-ASCII"},
		{"tab", "my\tapp", "control or non-ASCII"},
		{"non-ascii", "café", "control or non-ASCII"},
		{"absolute path", "/etc/passwd", "path separator"},
		{"relative path", "../evil", "path separator"},
		{"windows path", `..\evil`, "path separator"},
		{"backslash", `my\app`, "path separator"},
		{"parent directory", "..", "relative path element"},
		{"current directory", ".", "relative path element"},
		{"dotfile", ".hidden", "starts with a dot"},
		{"trailing dot", "myapp.", "ends with a dot or a space"},
		{"trailing space", "myapp ", "ends with a dot or a space"},
		{"leading space", " myapp", "starts with a space"},
		{"device con", "con", "reserved Windows device name"},
		{"device nul", "nul", "reserved Windows device name"},
		{"device aux", "aux", "reserved Windows device name"},
		{"device prn", "prn", "reserved Windows device name"},
		{"device com1", "com1", "reserved Windows device name"},
		{"device lpt9", "lpt9", "reserved Windows device name"},
		{"device uppercase", "CON", "reserved Windows device name"},
		{"device with extension", "con.txt", "reserved Windows device name"},
		{"uppercase", "MyApp", "uppercase letter"},
		{"go keyword func", "func", "Go reserved word"},
		{"go keyword go", "go", "Go reserved word"},
		{"go keyword range", "range", "Go reserved word"},
		{"leading digit", "2fast", "must start with a lowercase letter"},
		{"leading hyphen", "-app", "must start with a lowercase letter"},
		{"trailing hyphen", "app-", "must start with a lowercase letter"},
		{"double hyphen", "my--app", "must start with a lowercase letter"},
		{"underscore", "my_app", "must start with a lowercase letter"},
		{"dot inside", "my.app", "must start with a lowercase letter"},
		{"at sign", "my@app", "must start with a lowercase letter"},
		{"colon", "my:app", "must start with a lowercase letter"},
	}

	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			err := generator.ValidateName(tc.name)
			if err == nil {
				t.Fatalf("ValidateName(%q) = nil, want an error", tc.name)
			}

			var nameErr *generator.NameError
			if !errors.As(err, &nameErr) {
				t.Fatalf("ValidateName(%q) returned %T, want *generator.NameError", tc.name, err)
			}
			if !strings.Contains(nameErr.Problem, tc.problem) {
				t.Errorf("problem = %q, want it to contain %q", nameErr.Problem, tc.problem)
			}
			if nameErr.Action == "" {
				t.Errorf("Action is empty; every rejection must name a fix")
			}
			if nameErr.Kind != "project name" {
				t.Errorf("Kind = %q, want %q", nameErr.Kind, "project name")
			}
			// The rendered message must never carry the raw bytes of a
			// hostile name: %q escapes every control character, so a name
			// containing a newline cannot forge a second output line.
			if strings.ContainsAny(err.Error(), "\x00\n\r\x1b\t") {
				t.Errorf("Error() = %q leaks a raw control character", err.Error())
			}
		})
	}
}

func TestValidateModulePathAccepts(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"myapp",
		"github.com/you/myapp",
		"git.example.com/team/sub/myapp",
		"gitlab.com/Group/Repo",
		"example.com/my-app.v2",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			if err := generator.ValidateModulePath(path); err != nil {
				t.Fatalf("ValidateModulePath(%q) = %v, want nil", path, err)
			}
		})
	}
}

func TestValidateModulePathRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		label   string
		path    string
		problem string
	}{
		{"empty", "", "is empty"},
		{"too long", strings.Repeat("a", generator.MaxModulePathLen+1), "longer than"},
		{"nul byte", "github.com/you/my\x00app", "control or non-ASCII"},
		{"newline", "github.com/you\nmyapp", "control or non-ASCII"},
		{"space", "github.com/you/my app", "not a valid Go module path"},
		{"backslash", `github.com\you\myapp`, "backslash"},
		{"leading slash", "/github.com/you", "empty path element"},
		{"trailing slash", "github.com/you/", "empty path element"},
		{"doubled slash", "github.com//you", "empty path element"},
		{"parent element", "github.com/../you", `"." or ".." path element`},
		{"dot element", "github.com/./you", `"." or ".." path element`},
		{"invalid character", "github.com/you/my!app", "not a valid Go module path"},
	}

	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			err := generator.ValidateModulePath(tc.path)
			if err == nil {
				t.Fatalf("ValidateModulePath(%q) = nil, want an error", tc.path)
			}

			var nameErr *generator.NameError
			if !errors.As(err, &nameErr) {
				t.Fatalf("ValidateModulePath(%q) returned %T, want *generator.NameError", tc.path, err)
			}
			if !strings.Contains(nameErr.Problem, tc.problem) {
				t.Errorf("problem = %q, want it to contain %q", nameErr.Problem, tc.problem)
			}
			if nameErr.Action == "" {
				t.Errorf("Action is empty; every rejection must name a fix")
			}
			if nameErr.Kind != "module path" {
				t.Errorf("Kind = %q, want %q", nameErr.Kind, "module path")
			}
		})
	}
}
