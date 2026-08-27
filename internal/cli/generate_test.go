package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
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

// fakeGenerator lets tests observe exactly what a Generator method
// received, and control what it returns, without depending on
// notImplementedGenerator's fixed behavior.
type fakeGenerator struct {
	featureCalls  []string
	resourceCalls []string
	err           error
}

func (f *fakeGenerator) GenerateFeature(_ context.Context, _, name string) error {
	f.featureCalls = append(f.featureCalls, name)
	return f.err
}

func (f *fakeGenerator) GenerateResource(_ context.Context, _, name string) error {
	f.resourceCalls = append(f.resourceCalls, name)
	return f.err
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

func TestGenerateFeatureValidNameDelegatesAndSurfacesNotImplemented(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerator{err: &NotImplementedError{Milestone: "M7", Kind: "feature"}}
	tree := []*Command{{Name: "generate", Subcommands: []*Command{generateFeatureCommand(gen)}}}

	code, stdout, stderr := runGenerateFixture(t, tree, []string{"generate", "feature", "Invoice"})
	if code != int(clierr.ExitError) {
		t.Errorf("exit code = %d, want %d (ExitError)", code, clierr.ExitError)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty on failure in human mode", stdout)
	}
	if !strings.Contains(stderr, "not implemented") || !strings.Contains(stderr, "M7") {
		t.Errorf("stderr = %q, want an honest not-implemented message naming M7", stderr)
	}
	if len(gen.featureCalls) != 1 || gen.featureCalls[0] != "invoice" {
		t.Errorf("featureCalls = %v, want exactly one call with the canonicalized name %q", gen.featureCalls, "invoice")
	}
}

func TestGenerateResourceValidNameDelegatesAndSurfacesNotImplemented(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerator{err: &NotImplementedError{Milestone: "M7", Kind: "resource"}}
	tree := []*Command{{Name: "generate", Subcommands: []*Command{generateResourceCommand(gen)}}}

	code, _, stderr := runGenerateFixture(t, tree, []string{"generate", "resource", "Order"})
	if code != int(clierr.ExitError) {
		t.Errorf("exit code = %d, want %d (ExitError); stderr=%s", code, clierr.ExitError, stderr)
	}
	if !strings.Contains(stderr, "resource") || !strings.Contains(stderr, "M7") {
		t.Errorf("stderr = %q, want it to name the resource kind and milestone M7", stderr)
	}
	if len(gen.resourceCalls) != 1 || gen.resourceCalls[0] != "order" {
		t.Errorf("resourceCalls = %v, want exactly one call with %q", gen.resourceCalls, "order")
	}
}

func TestGenerateResourceJSONModeNeverFakesSuccess(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerator{err: &NotImplementedError{Milestone: "M7", Kind: "resource"}}
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
	if env.Error.Code != "generate.not_implemented" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "generate.not_implemented")
	}
	if !strings.Contains(env.Error.Message, "M7") {
		t.Errorf("error.message = %q, want it to name milestone M7", env.Error.Message)
	}
}

func TestGenerateCommandRealRegistryHonestlyFails(t *testing.T) {
	// Exercises the actual production wiring (generateCommand(), which
	// wires in notImplementedGenerator{}), not a fake, to prove the real
	// registry entry never fakes success end to end.
	t.Parallel()
	tree := []*Command{generateCommand()}

	for _, kind := range []string{"feature", "resource"} {
		code, stdout, stderr := runGenerateFixture(t, tree, []string{"generate", kind, "invoice"})
		if code == int(clierr.ExitOK) {
			t.Errorf("generate %s: exit code = 0, want non-zero (never fake success)", kind)
		}
		if stdout != "" {
			t.Errorf("generate %s: stdout = %q, want empty (no success output on failure)", kind, stdout)
		}
		if !strings.Contains(stderr, "not implemented") {
			t.Errorf("generate %s: stderr = %q, want an honest not-implemented message", kind, stderr)
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

func TestNotImplementedErrorMessage(t *testing.T) {
	t.Parallel()
	err := &NotImplementedError{Milestone: "M7", Kind: "feature"}
	var target *NotImplementedError
	if !errors.As(error(err), &target) {
		t.Fatal("errors.As failed on *NotImplementedError itself")
	}
	if !strings.Contains(err.Error(), "M7") || !strings.Contains(err.Error(), "feature") {
		t.Errorf("Error() = %q, want it to mention the milestone and kind", err.Error())
	}
}
