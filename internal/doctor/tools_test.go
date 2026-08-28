package doctor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeRunner is a Runner that returns canned output or errors keyed by the
// command name it would have invoked, so tests can exercise the decision
// logic in tools.go and recipe.go without depending on any real binary
// being installed, at any particular version, on the machine running the
// test.
type fakeRunner struct {
	// outputs maps "name arg1 arg2..." (space-joined) to the output that
	// invocation should return.
	outputs map[string]string
	// errs maps the same key to an error that invocation should return
	// instead of succeeding.
	errs map[string]error
	// calls records every invocation, in order, for assertions that care
	// about what doctor actually ran (not just what it concluded).
	calls []string
}

func (f *fakeRunner) key(name string, args ...string) string {
	k := name
	for _, a := range args {
		k += " " + a
	}
	return k
}

func (f *fakeRunner) Run(_ context.Context, _ time.Duration, name string, args ...string) (string, error) {
	k := f.key(name, args...)
	f.calls = append(f.calls, k)
	if err, ok := f.errs[k]; ok {
		return f.outputs[k], err
	}
	if out, ok := f.outputs[k]; ok {
		return out, nil
	}
	return "", errors.New("fakeRunner: no stub for " + k)
}

func TestCheckGo(t *testing.T) {
	t.Parallel()

	t.Run("ok", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{outputs: map[string]string{
			"go version": "go version go1.26.5-X:nodwarf5 linux/amd64\n",
		}}
		got := checkGo(context.Background(), r, time.Second)
		if got.Status != StatusOK {
			t.Fatalf("Status = %v, want %v (Found=%q Remedy=%q)", got.Status, StatusOK, got.Found, got.Remedy)
		}
		if got.Found != "go 1.26.5" {
			t.Errorf("Found = %q, want %q", got.Found, "go 1.26.5")
		}
	})

	t.Run("too old fails, not warns", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{outputs: map[string]string{
			"go version": "go version go1.20.0 linux/amd64\n",
		}}
		got := checkGo(context.Background(), r, time.Second)
		if got.Status != StatusFail {
			t.Fatalf("Status = %v, want %v", got.Status, StatusFail)
		}
		if got.Remedy == "" {
			t.Error("Remedy is empty for a failing check")
		}
	})

	t.Run("missing binary fails with the runner's error surfaced", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{errs: map[string]error{
			"go version": errors.New(`exec: "go": executable file not found in $PATH`),
		}}
		got := checkGo(context.Background(), r, time.Second)
		if got.Status != StatusFail {
			t.Fatalf("Status = %v, want %v", got.Status, StatusFail)
		}
		if got.Found == "" {
			t.Error("Found is empty; a fail check must say what was found")
		}
	})

	t.Run("unparsable output fails rather than reporting ok", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{outputs: map[string]string{
			"go version": "not actually go\n",
		}}
		got := checkGo(context.Background(), r, time.Second)
		if got.Status != StatusFail {
			t.Fatalf("Status = %v, want %v", got.Status, StatusFail)
		}
	})
}

func TestCheckNode(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{outputs: map[string]string{"node --version": "v22.22.2\n"}}
	got := checkNode(context.Background(), r, time.Second)
	if got.Status != StatusOK || got.Found != "node 22.22.2" {
		t.Errorf("got %+v", got)
	}
}

func TestCheckPnpm(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{outputs: map[string]string{"pnpm --version": "10.33.0\n"}}
	got := checkPnpm(context.Background(), r, time.Second)
	if got.Status != StatusOK || got.Found != "pnpm 10.33.0" {
		t.Errorf("got %+v", got)
	}
}

// TestCheckGeneratorToolOutsideGeneratedProject covers behavior inside the
// Nise framework repository itself (insideGeneratedProject=false), where
// go.mod genuinely pins these three tools and doctor must still catch a
// missing or unresolvable one — unchanged from before fix round 2.
func TestCheckGeneratorToolOutsideGeneratedProject(t *testing.T) {
	t.Parallel()

	t.Run("sqlc ok, no version floor enforced", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{outputs: map[string]string{"go tool sqlc version": "v1.31.1\n"}}
		got := checkSqlc(context.Background(), r, time.Second, false)
		if got.Status != StatusOK {
			t.Errorf("Status = %v, want %v", got.Status, StatusOK)
		}
	})

	t.Run("goose ok, prefixed output", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{outputs: map[string]string{"go tool goose --version": "goose version: v3.27.3\n"}}
		got := checkGoose(context.Background(), r, time.Second, false)
		if got.Status != StatusOK {
			t.Errorf("Status = %v, want %v", got.Status, StatusOK)
		}
	})

	t.Run("oapi-codegen ok, two-line output", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{outputs: map[string]string{
			"go tool oapi-codegen -version": "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen\nv2.8.0\n",
		}}
		got := checkOapiCodegen(context.Background(), r, time.Second, false)
		if got.Status != StatusOK {
			t.Errorf("Status = %v, want %v", got.Status, StatusOK)
		}
	})

	t.Run("missing tool still fails even with no version floor", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{errs: map[string]error{
			"go tool sqlc version": errors.New("go: tool sqlc not found"),
		}}
		got := checkSqlc(context.Background(), r, time.Second, false)
		if got.Status != StatusFail {
			t.Fatalf("Status = %v, want %v", got.Status, StatusFail)
		}
		if got.Remedy == "" {
			t.Error("Remedy is empty for a failing check")
		}
	})
}

func TestSQLCDoesNotRunImplicitlyInsideGeneratedProject(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{}
	got := checkSqlc(context.Background(), r, time.Second, true)
	if got.Status != StatusSkipped {
		t.Fatalf("Status = %v, want %v (Found=%q)", got.Status, StatusSkipped, got.Found)
	}
	if slices.Contains(r.calls, "go tool sqlc version") {
		t.Fatalf("calls = %v, sqlc may download and must not run implicitly", r.calls)
	}
	if !strings.Contains(got.Required, "make sqlc-compile") {
		t.Errorf("Required = %q, want explicit verification command", got.Required)
	}
}

// TestFutureGeneratorToolsSkipInsideGeneratedProject is fix round 2's
// regression coverage: inside a generated project, tools not pinned there
// yet must report StatusSkipped — never
// StatusFail, and never even attempt to run the tool at all, since a
// generated project's go.mod is not expected to declare them until their
// M3/M4 tasks land.
// The fakeRunner below has NO stubs for any "go tool ..." invocation on
// purpose: if checkGeneratorTool ever called Runner.Run in this branch,
// fakeRunner.Run's "no stub for ..." fallback error would surface as a
// StatusFail, failing these tests loudly rather than silently.
func TestFutureGeneratorToolsSkipInsideGeneratedProject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   func(*fakeRunner) Check
		key  string
	}{
		{"goose", func(r *fakeRunner) Check { return checkGoose(context.Background(), r, time.Second, true) }, "go tool goose --version"},
		{"oapi-codegen", func(r *fakeRunner) Check { return checkOapiCodegen(context.Background(), r, time.Second, true) }, "go tool oapi-codegen -version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &fakeRunner{}
			got := tt.fn(r)

			if got.Status != StatusSkipped {
				t.Errorf("Status = %v, want %v", got.Status, StatusSkipped)
			}
			if got.Name != tt.name {
				t.Errorf("Name = %q, want %q", got.Name, tt.name)
			}
			if got.Remedy != "" {
				t.Errorf("Remedy = %q, want empty: there is nothing to fix, and the old remedy (add a go.mod tool directive) actively corrupts a generated application's go.mod", got.Remedy)
			}
			if got.Found == "" || got.Required == "" {
				t.Errorf("Found/Required must still explain the skip: Found=%q Required=%q", got.Found, got.Required)
			}
			for _, call := range r.calls {
				if call == tt.key {
					t.Errorf("checkGeneratorTool invoked %q inside a generated project; it must not run the tool at all", tt.key)
				}
			}
		})
	}
}

func TestCheckContainerRuntime(t *testing.T) {
	t.Parallel()

	t.Run("real docker reports as docker", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{outputs: map[string]string{
			"docker --version": "Docker version 29.6.2, build dfc4efb\n",
		}}
		got := checkContainerRuntime(context.Background(), r, time.Second)
		if got.Status != StatusOK {
			t.Fatalf("Status = %v, want %v", got.Status, StatusOK)
		}
		if got.Found != "docker 29.6.2" {
			t.Errorf("Found = %q, want %q", got.Found, "docker 29.6.2")
		}
	})

	t.Run("docker binary is actually a podman shim, and doctor says so", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{outputs: map[string]string{
			// This is the exact shape podman's docker-compatibility shim
			// prints when invoked as "docker" on a machine that has no
			// real Docker Engine installed.
			"docker --version": "podman version 5.8.4\n",
		}}
		got := checkContainerRuntime(context.Background(), r, time.Second)
		if got.Status != StatusOK {
			t.Fatalf("Status = %v, want %v", got.Status, StatusOK)
		}
		if got.Found != "podman 5.8.4 (invoked as \"docker\", which is a podman shim on this machine)" {
			t.Errorf("Found = %q, did not correctly identify the podman shim", got.Found)
		}
	})

	t.Run("no docker, real podman found", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{
			errs:    map[string]error{"docker --version": errors.New(`exec: "docker": executable file not found in $PATH`)},
			outputs: map[string]string{"podman --version": "podman version 5.8.4\n"},
		}
		got := checkContainerRuntime(context.Background(), r, time.Second)
		if got.Status != StatusOK {
			t.Fatalf("Status = %v, want %v", got.Status, StatusOK)
		}
		if got.Found != "podman 5.8.4" {
			t.Errorf("Found = %q, want %q", got.Found, "podman 5.8.4")
		}
	})

	t.Run("neither present fails, required tool missing", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{errs: map[string]error{
			"docker --version": errors.New(`exec: "docker": executable file not found in $PATH`),
			"podman --version": errors.New(`exec: "podman": executable file not found in $PATH`),
		}}
		got := checkContainerRuntime(context.Background(), r, time.Second)
		if got.Status != StatusFail {
			t.Fatalf("Status = %v, want %v", got.Status, StatusFail)
		}
		if got.Remedy == "" {
			t.Error("Remedy is empty for a failing required check")
		}
	})
}
