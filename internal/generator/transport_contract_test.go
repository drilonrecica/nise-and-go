package generator_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDefinesTheTransportContractSuite(t *testing.T) {
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
		"internal/platform/httpapi/transport_test.go": {
			"TestTransportContractRoutesAndNegotiation",
			"TestTransportContractStrictInput",
			"TestTransportContractCursorPagination",
			"TestTransportContractReportPagination",
			"TestTransportContractBoundsTheResponse",
			"TestTransportContractIsDeterministic",
			"func FuzzTransportSurface(f *testing.F)",
			"deps.RegisterAPI = func(r chi.Router)",
			"assertAPIResponseInvariants",
		},
		"internal/platform/httpapi/router.go": {
			"const APIPathPrefix = \"/api/v1\"",
			"func apiPathRemainder(path string) (string, bool)",
			"func unsupportedMethod(apiCore, documentCore http.Handler) http.HandlerFunc",
			"root.MethodNotAllowed(unsupportedMethod(apiCore, documentCore))",
		},
		"internal/platform/httpapi/router_test.go": {
			"TestRouterKeepsUnknownMethodsInsideTheSecurityCore",
			`"PROPFIND"`,
		},
		"internal/platform/httpapi/httpjson/json_test.go": {
			"func FuzzDecode(f *testing.F)",
		},
		"internal/platform/idempotency/idempotency_test.go": {
			"func FuzzValidateKey(f *testing.F)",
			"func FuzzFingerprint(f *testing.F)",
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

// TestGeneratedTransportContractPassesInAFreshProject runs the generated
// transport suite in a project built the way a user's would be. The fragment
// assertions above prove the file is written; only running it proves the
// contract it states is actually true of the code that ships.
func TestGeneratedTransportContractPassesInAFreshProject(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: this test resolves the generated project's dependencies and runs its test suite")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("skipping: no go binary on PATH: %v", err)
	}

	root := filepath.Join(t.TempDir(), "transportprobe")
	if _, err := generator.Write(root, generator.Options{
		Name:       "transportprobe",
		ModulePath: "example.com/transportprobe",
		CLIVersion: fixedVersion,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	run := func(command string, args ...string) (string, error) {
		cmd := exec.Command(command, args...) // #nosec G204 -- command and arguments are fixed by this test.
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GOTOOLCHAIN=local",
			"GOFLAGS=-mod=mod -buildvcs=false",
			// The transport suite is entirely in-memory. Clearing the
			// database URL keeps the PostgreSQL-backed suites skipping
			// rather than failing, and CI=false keeps that a skip.
			"TEST_DATABASE_URL=",
			"CI=false",
		)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run(goBin, "mod", "edit", "-replace", generator.NiseModulePath+"="+findFrameworkRoot(t)); err != nil {
		t.Fatalf("go mod edit -replace: %v\n%s", err, out)
	}
	if out, err := run(goBin, "mod", "tidy"); err != nil {
		t.Skipf("skipping: generated dependencies could not be resolved: %v\n%s", err, out)
	}

	// -run selects the suites that need nothing but the process itself; the
	// seed corpus of every fuzz target in the package runs with them.
	if out, err := run(goBin, "test", "-count=1",
		"./internal/platform/httpapi/...",
		"./internal/platform/idempotency/...",
	); err != nil {
		t.Fatalf("generated transport contract suite: %v\n%s", err, out)
	}
}
