package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDefinesPasswordHashing(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	owners := make(map[string]generator.Ownership, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
		owners[file.Path] = file.Owner
	}

	wants := map[string][]string{
		"internal/platform/passwords/passwords.go": {
			`"github.com/drilonrecica/nise-and-go/runtime/password"`,
			"func Policy() (*password.Policy, error)",
			"func current() (password.Params, error)",
			"func superseded() ([]password.Params, error)",
			"password.NewPolicy(currentParams, previous...)",
		},
		"internal/platform/passwords/passwords_test.go": {
			"TestPolicyIsConstructible",
			"TestPolicyVersionsAreDistinctAndCurrentIsHighest",
			"TestPolicyHashesAndVerifies",
		},
		"internal/app/password.go": {
			"func RunPasswordBenchmark(request PasswordBenchmarkRequest) (PasswordBenchmarkResult, error)",
			"password.Recommend(request.MemoryKiB, request.Parallelism, request.Target, request.Samples)",
			"DefaultBenchmarkTarget      = 250 * time.Millisecond",
		},
		"cmd/myapp/main.go": {
			`args[0] == "password"`,
			"func parsePasswordArgs(args []string) (request app.PasswordBenchmarkRequest, jsonOutput bool, err error)",
			`fs.Uint("memory"`,
			`fs.Duration("target"`,
		},
		"cmd/myapp/main_test.go": {
			"TestParsePasswordArgs",
			"TestPasswordBenchmarkNeedsNoConfiguration",
		},
		"go.mod": {
			"golang.org/x/crypto " + generator.XCryptoVersion,
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The parameter history is application-owned: an upgrade that rewrote it
	// would silently change which stored hashes still verify.
	for _, path := range []string{
		"internal/platform/passwords/passwords.go",
		"internal/platform/passwords/passwords_test.go",
		"internal/app/password.go",
	} {
		if owners[path] != generator.OwnerApp {
			t.Errorf("%s owner = %q, want %q", path, owners[path], generator.OwnerApp)
		}
	}

	// The benchmark must not reach configuration or the database: it has to
	// be runnable on a production host without production credentials.
	for _, forbidden := range []string{"config.Load", "database.Open", "pgx"} {
		if strings.Contains(content["internal/app/password.go"], forbidden) {
			t.Errorf("the password benchmark reaches %q", forbidden)
		}
	}
}
