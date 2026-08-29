package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectExposesExplicitMigrationCommandsAndCompatibilityGate(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	byPath := make(map[string]string, len(files))
	for _, file := range files {
		byPath[file.Path] = string(file.Content)
	}

	for _, path := range []string{
		"internal/platform/database/compatibility.go",
		"internal/platform/database/compatibility_test.go",
		"internal/app/database.go",
		"cmd/myapp/main_test.go",
	} {
		if _, ok := byPath[path]; !ok {
			t.Errorf("generated project lacks %s", path)
		}
	}
	for path, contract := range map[string]string{
		"cmd/myapp/main.go":                           "db migrate",
		"internal/app/app.go":                         "CheckCompatibility",
		"internal/app/database.go":                    "RunDatabase",
		"internal/platform/database/compatibility.go": "SchemaAhead",
	} {
		if !strings.Contains(byPath[path], contract) {
			t.Errorf("%s lacks %q", path, contract)
		}
	}
}
