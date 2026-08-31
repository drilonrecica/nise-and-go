package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/generator/feature"
)

func TestValidateResourceNameAccepts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"order", "order"},
		{"Order", "order"},
		{"ORDER", "order"},
		{"invoice2", "invoice2"},
		{"a", "a"},
		// A name that merely starts with a Windows reserved device name is
		// not itself reserved: "console" and "nullable" are legal
		// directory names on Windows, so the check below must match the
		// whole canonicalized name, never a prefix.
		{"console", "console"},
		{"nullable", "nullable"},
		{"comedy", "comedy"},
		{"lpt10", "lpt10"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateResourceName(tt.in)
			if err != nil {
				t.Fatalf("ValidateResourceName(%q) = %v, want no error", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ValidateResourceName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidateResourceNameRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"underscore", "invoice_line"},
		{"hyphen", "invoice-line"},
		{"space", "invoice line"},
		{"leading digit", "1invoice"},
		{"too long", strings.Repeat("a", maxResourceNameLen+1)},
		{"go keyword", "type"},
		{"go keyword uppercase", "Type"},
		{"reserved core service", "auth"},
		{"reserved core service uppercase", "Authz"},
		{"reserved excluded dir", "util"},
		{"reserved ownership dir", "store"},
		{"reserved ownership dir 2", "embedded"},
		{"path separator", "invoice/line"},
		{"path separator backslash", `invoice\line`},
		{"dot dot", ".."},
		{"embedded newline", "invoice\nline"},
		{"windows device name con", "con"},
		{"windows device name con uppercase", "CON"},
		{"windows device name con mixed case", "Con"},
		{"windows device name prn", "prn"},
		{"windows device name aux", "aux"},
		{"windows device name nul", "nul"},
		{"windows device name com1", "com1"},
		{"windows device name com9", "com9"},
		{"windows device name lpt1", "lpt1"},
		{"windows device name lpt9 uppercase", "LPT9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ValidateResourceName(tt.in); err == nil {
				t.Errorf("ValidateResourceName(%q) = nil error, want a rejection", tt.in)
			}
		})
	}
}

func TestValidateResourceNameNonASCIIIsRejected(t *testing.T) {
	t.Parallel()
	if _, err := ValidateResourceName("café"); err == nil {
		t.Error("ValidateResourceName(\"café\") = nil error, want a rejection (non-ASCII)")
	}
}

// fakeGenerator lets tests observe exactly what a Generator method received,
// and control what it returns, without touching a filesystem.
type fakeGenerator struct {
	featureCalls  []string
	resourceCalls []string
	plan          feature.Plan
	err           error
}

func (f *fakeGenerator) GenerateFeature(_ context.Context, _, name string) (feature.Plan, error) {
	f.featureCalls = append(f.featureCalls, name)
	return f.plan, f.err
}

func (f *fakeGenerator) GenerateResource(_ context.Context, _, name string) (feature.Plan, error) {
	f.resourceCalls = append(f.resourceCalls, name)
	return f.plan, f.err
}

func runGenerateFixture(t *testing.T, tree []*Command, args []string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = execute(args, IO{Stdout: &out, Stderr: &errOut, Getenv: emptyGetenv}, tree)
	return code, out.String(), errOut.String()
}

func TestGenerateFeatureRequiresExactlyOneArgument(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerator{}
	tree := []*Command{{Name: "generate", Subcommands: []*Command{generateFeatureCommand(gen)}}}

	for _, args := range [][]string{
		{"generate", "feature"},
		{"generate", "feature", "invoice", "extra"},
	} {
		code, _, stderr := runGenerateFixture(t, tree, args)
		if code != int(clierr.ExitUsage) {
			t.Errorf("args=%v: exit code = %d, want %d (ExitUsage); stderr=%s", args, code, clierr.ExitUsage, stderr)
		}
		if len(gen.featureCalls) != 0 {
			t.Errorf("args=%v: Generator was called despite bad arity", args)
		}
	}
}

func TestGenerateFeatureRejectsInvalidName(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerator{}
	tree := []*Command{{Name: "generate", Subcommands: []*Command{generateFeatureCommand(gen)}}}

	code, _, stderr := runGenerateFixture(t, tree, []string{"generate", "feature", "invoice_line"})
	if code != int(clierr.ExitUsage) {
		t.Errorf("exit code = %d, want %d (ExitUsage); stderr=%s", code, clierr.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "invoice_line") {
		t.Errorf("stderr = %q, want it to name the rejected value", stderr)
	}
	if len(gen.featureCalls) != 0 {
		t.Error("Generator was called despite an invalid name")
	}
}

func TestGenerateFeatureRejectsReservedName(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerator{}
	tree := []*Command{{Name: "generate", Subcommands: []*Command{generateFeatureCommand(gen)}}}

	code, _, stderr := runGenerateFixture(t, tree, []string{"generate", "feature", "auth"})
	if code != int(clierr.ExitUsage) {
		t.Errorf("exit code = %d, want %d (ExitUsage); stderr=%s", code, clierr.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "reserved") {
		t.Errorf("stderr = %q, want it to explain the name is reserved", stderr)
	}
}

func TestGenerateFeatureValidNameDelegatesCanonicalized(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerator{}
	tree := []*Command{{Name: "generate", Subcommands: []*Command{generateFeatureCommand(gen)}}}

	code, stdout, stderr := runGenerateFixture(t, tree, []string{"generate", "feature", "Invoice"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Created feature") {
		t.Errorf("stdout = %q, want it to report what was created", stdout)
	}
	// The name reaches the generator canonicalized, so a leading capital is
	// accepted and the directory it becomes is still lowercase.
	if len(gen.featureCalls) != 1 || gen.featureCalls[0] != "invoice" {
		t.Errorf("featureCalls = %v, want exactly one call with %q", gen.featureCalls, "invoice")
	}
}

func TestGenerateRefusesToWriteOverAnExistingSlice(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerator{err: &feature.ExistsError{Paths: []string{"internal/features/order/domain.go"}}}
	tree := []*Command{{Name: "generate", Subcommands: []*Command{generateResourceCommand(gen)}}}

	code, stdout, stderr := runGenerateFixture(t, tree, []string{"generate", "resource", "Order"})
	if code != int(clierr.ExitError) {
		t.Errorf("exit code = %d, want %d (ExitError); stderr=%s", code, clierr.ExitError, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty on failure in human mode", stdout)
	}
	// A generated slice is application-owned from the moment it is written, so
	// the command refuses rather than replacing it — and names what is there.
	if !strings.Contains(stderr, "already exists") || !strings.Contains(stderr, "domain.go") {
		t.Errorf("stderr = %q, want it to say the slice exists and name the path", stderr)
	}
	if !strings.Contains(stderr, "nothing was written") {
		t.Errorf("stderr = %q, want it to say nothing was written", stderr)
	}
}

func TestGenerateResourceJSONModeReportsARefusalAsAnError(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerator{err: &feature.ExistsError{Paths: []string{"internal/features/order/domain.go"}}}
	tree := []*Command{{Name: "generate", Subcommands: []*Command{generateResourceCommand(gen)}}}

	code, stdout, stderr := runGenerateFixture(t, tree, []string{"generate", "resource", "Order", "--json"})
	if code != int(clierr.ExitError) {
		t.Errorf("exit code = %d, want %d (ExitError)", code, clierr.ExitError)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty in JSON mode", stderr)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &env); err != nil {
		t.Fatalf("stdout is not a valid JSON error envelope: %v\nstdout=%s", err, stdout)
	}
	if env.Error.Code != "generate.exists" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "generate.exists")
	}
}

func TestGenerateCommandRealRegistryRefusesOutsideAProject(t *testing.T) {
	// Exercises the actual production wiring rather than a fake. The working
	// directory of a test run is this repository, which has a go.mod but is
	// not a generated application; what matters is that the command reports a
	// failure rather than writing into whatever it was pointed at.
	tree := []*Command{generateCommand()}
	t.Chdir(t.TempDir())

	for _, kind := range []string{"feature", "resource"} {
		code, stdout, stderr := runGenerateFixture(t, tree, []string{"generate", kind, "invoice"})
		if code == int(clierr.ExitOK) {
			t.Errorf("generate %s in an empty directory: exit code = 0, want non-zero", kind)
		}
		if stdout != "" {
			t.Errorf("generate %s: stdout = %q, want empty on failure", kind, stdout)
		}
		if !strings.Contains(stderr, "go.mod") {
			t.Errorf("generate %s: stderr = %q, want it to say this is not a Go project", kind, stderr)
		}
	}
}

func TestGenerateBareCommandShowsHelpNotAnError(t *testing.T) {
	t.Parallel()
	tree := []*Command{generateCommand()}
	code, _, stderr := runGenerateFixture(t, tree, []string{"generate"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0 (bare group command shows help); stderr=%s", code, stderr)
	}
}

func TestGenerateCommandHasNoReservedFlagCollisions(t *testing.T) {
	t.Parallel()
	checkNoReservedFlagCollisions(t, nil, []*Command{generateCommand()})
}
