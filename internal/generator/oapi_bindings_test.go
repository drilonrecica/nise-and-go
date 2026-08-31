package generator_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDefinesStrictOpenAPIBindings(t *testing.T) {
	t.Parallel()
	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
	}

	wants := map[string][]string{
		"api/openapi.yaml": {
			"openapi: 3.0.3",
			"url: /api/v1",
			"operationId: getAPIIndex",
		},
		"api/oapi-codegen.yaml": {
			"package: openapigen",
			"chi-server: true",
			"strict-server: true",
			"models: true",
		},
		"internal/platform/httpapi/openapigen/openapi.gen.go": {
			"type StrictServerInterface interface",
			"GetAPIIndex(ctx context.Context",
			"func NewStrictHandler(",
			"func HandlerFromMux(",
		},
		"internal/platform/httpapi/api.go": {
			"//go:generate go tool oapi-codegen",
			"var _ openapigen.StrictServerInterface = (*Server)(nil)",
			"func (s *Server) Register(r chi.Router)",
			"openapigen.NewStrictHandlerWithOptions",
		},
		"internal/platform/httpapi/api_test.go": {
			"TestStrictOpenAPIIndexRoute",
			`"/api/v1/"`,
		},
		"internal/app/app.go": {
			"apiServer, err := httpapi.NewServer(cursors)",
			"RegisterAPI: apiServer.Register",
		},
		"Makefile": {
			"api-generate:",
			"api-check:",
			"go tool oapi-codegen",
		},
		"go.mod": {
			"github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen",
			"github.com/oapi-codegen/oapi-codegen/v2 " + generator.OAPICodegenVersion,
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}
}

func TestGeneratedOpenAPIBindingsAreReproducible(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: this test resolves and runs the generated project's pinned oapi-codegen tool")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("skipping: no go binary on PATH: %v", err)
	}
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skipf("skipping: no make binary on PATH: %v", err)
	}

	root := filepath.Join(t.TempDir(), "oapiprobe")
	if _, err := generator.Write(root, generator.Options{
		Name:       "oapiprobe",
		ModulePath: "example.com/oapiprobe",
		CLIVersion: fixedVersion,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	run := func(command string, args ...string) (string, error) {
		cmd := exec.Command(command, args...) // #nosec G204 -- command and arguments are fixed by this test.
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod -buildvcs=false", "CI=false")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run(goBin, "mod", "edit", "-replace", generator.NiseModulePath+"="+findFrameworkRoot(t)); err != nil {
		t.Fatalf("go mod edit -replace: %v\n%s", err, out)
	}
	if out, err := run(goBin, "mod", "tidy"); err != nil {
		t.Skipf("skipping: generated dependencies could not be resolved: %v\n%s", err, out)
	}
	if out, err := run(goBin, "tool", "oapi-codegen", "-version"); err != nil {
		t.Fatalf("go tool oapi-codegen -version: %v\n%s", err, out)
	} else if !strings.Contains(out, generator.OAPICodegenVersion) {
		t.Fatalf("oapi-codegen version output = %q, want %s", out, generator.OAPICodegenVersion)
	}

	generatedPath := filepath.Join(root, "internal", "platform", "httpapi", "openapigen", "openapi.gen.go")
	original, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("ReadFile generated binding: %v", err)
	}
	if out, err := run(makeBin, "api-check"); err != nil {
		t.Fatalf("clean make api-check: %v\n%s", err, out)
	}

	corrupted := append(bytes.Clone(original), []byte("\n// stale probe\n")...)
	// #nosec G703 -- generatedPath is a fixed child of this test's t.TempDir project.
	if err := os.WriteFile(generatedPath, corrupted, generator.FileMode); err != nil {
		t.Fatalf("corrupt generated binding: %v", err)
	}
	out, err := run(makeBin, "api-check")
	if err == nil {
		t.Fatal("make api-check accepted stale generated bindings")
	}
	if !strings.Contains(out, "is stale; run 'make api-generate'") {
		t.Fatalf("stale make api-check output = %q", out)
	}
	stillCorrupted, readErr := os.ReadFile(generatedPath)
	if readErr != nil {
		t.Fatalf("ReadFile after api-check: %v", readErr)
	}
	if !bytes.Equal(stillCorrupted, corrupted) {
		t.Fatal("api-check mutated the checked-in generated binding")
	}

	if out, err := run(makeBin, "api-generate"); err != nil {
		t.Fatalf("make api-generate: %v\n%s", err, out)
	}
	regenerated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("ReadFile regenerated binding: %v", err)
	}
	if !bytes.Equal(regenerated, original) {
		t.Fatal("api-generate did not reproduce the checked-in binding byte-for-byte")
	}
	if out, err := run(makeBin, "api-check"); err != nil {
		t.Fatalf("regenerated make api-check: %v\n%s", err, out)
	}

	// #nosec G703 -- generatedPath is a fixed child of this test's t.TempDir project.
	if err := os.WriteFile(generatedPath, corrupted, generator.FileMode); err != nil {
		t.Fatalf("corrupt generated binding before go generate: %v", err)
	}
	if out, err := run(goBin, "generate", "./internal/platform/httpapi"); err != nil {
		t.Fatalf("go generate HTTP API: %v\n%s", err, out)
	}
	regenerated, err = os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("ReadFile go-generate binding: %v", err)
	}
	if !bytes.Equal(regenerated, original) {
		t.Fatal("go generate did not reproduce the checked-in binding byte-for-byte")
	}

	specPath := filepath.Join(root, "api", "openapi.yaml")
	if err := os.WriteFile(specPath, []byte("openapi: [\n"), generator.FileMode); err != nil {
		t.Fatalf("write invalid OpenAPI probe: %v", err)
	}
	if out, err := run(makeBin, "api-generate"); err == nil {
		t.Fatal("api-generate accepted an invalid OpenAPI document")
	} else if !strings.Contains(out, "error loading swagger spec") {
		t.Fatalf("invalid api-generate output = %q", out)
	}
	afterInvalid, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("ReadFile after invalid generation: %v", err)
	}
	if !bytes.Equal(afterInvalid, original) {
		t.Fatal("failed generation damaged the last valid checked-in binding")
	}
}
