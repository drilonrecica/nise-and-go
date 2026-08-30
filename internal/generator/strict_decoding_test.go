package generator_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDefinesStrictJSONTransport(t *testing.T) {
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
		"api/oapi-codegen.yaml": {
			"user-templates:",
			"{{range .}}",
			"strict/strict-http.tmpl: |",
			"JSONBodyDecoder",
			"strict/strict-interface.tmpl: |",
			"JSONResponseSizeChecker",
			"RequestContentTypeMatcher",
			"RequestBodyAbsentChecker",
			"ErrUnsupportedRequestContentType",
		},
		"internal/platform/httpapi/httpjson/json.go": {
			"MaxRequestBodyBytes",
			"http.MaxBytesReader",
			"mime.ParseMediaType",
			"DisallowUnknownFields",
			"func Write(",
		},
		"internal/platform/httpapi/httpjson/json_test.go": {
			"TestDecode",
			"duplicate nested field",
			"TestWrite",
		},
		"internal/platform/httpapi/api.go": {
			"requestTransportError",
			"JSONBodyDecoder:           httpjson.Decode",
			"JSONResponseSizeChecker:   httpjson.CheckResponseSize",
			"RequestContentTypeMatcher: httpjson.MatchesMediaType",
			"RequestBodyAbsentChecker:  httpjson.IsBodyAbsent",
			"problem.RequestBodyTooLarge()",
			"problem.UnsupportedMediaType()",
		},
	}

	for path, fragments := range wants {
		body, exists := content[path]
		if !exists {
			t.Errorf("generated project lacks %s", path)
			continue
		}
		for _, fragment := range fragments {
			if !strings.Contains(body, fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}
}

func TestGeneratedStrictJSONTransportHandlesHostileInput(t *testing.T) {
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

	root := filepath.Join(t.TempDir(), "strictjsonprobe")
	if _, err := generator.Write(root, generator.Options{
		Name:       "strictjsonprobe",
		ModulePath: "example.com/strictjsonprobe",
		CLIVersion: fixedVersion,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	copyTestdata := func(name, destination string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("testdata", name)) // #nosec G304 -- name is a fixed test fixture.
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// #nosec G703 -- destination is a fixed child of this test's t.TempDir project.
		if err := os.WriteFile(filepath.Join(root, destination), data, generator.FileMode); err != nil {
			t.Fatalf("write %s: %v", destination, err)
		}
	}
	copyTestdata("strict-json-openapi.yaml", filepath.Join("api", "openapi.yaml"))
	copyTestdata("strict-json-probe_test.go.txt", filepath.Join("internal", "platform", "httpapi", "openapigen", "strictjson_probe_test.go"))

	run := func(command string, args ...string) (string, error) {
		cmd := exec.Command(command, args...) // #nosec G204 -- command and arguments are resolved above and argv is fixed.
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
	if out, err := run(makeBin, "api-generate"); err != nil {
		t.Fatalf("make api-generate: %v\n%s", err, out)
	}
	generated, err := os.ReadFile(filepath.Join(root, "internal", "platform", "httpapi", "openapigen", "openapi.gen.go"))
	if err != nil {
		t.Fatalf("read regenerated binding: %v", err)
	}
	for _, fragment := range []string{"JSONBodyDecoder", "JSONResponseSizeChecker"} {
		if !strings.Contains(string(generated), fragment) {
			t.Errorf("regenerated binding lacks %q", fragment)
		}
	}
	if out, err := run(goBin, "test", "./internal/platform/httpapi/httpjson", "./internal/platform/httpapi/openapigen"); err != nil {
		t.Fatalf("strict generated transport tests: %v\n%s", err, out)
	}
}
