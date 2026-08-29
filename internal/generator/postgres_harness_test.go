package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectUsesIsolatedPostgresTestHarness(t *testing.T) {
	t.Parallel()
	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	byPath := make(map[string]string, len(files))
	for _, file := range files {
		byPath[file.Path] = string(file.Content)
	}
	for path, contracts := range map[string][]string{
		"internal/platform/database/dbtest/database.go":      {"CREATE DATABASE", "CONNECTION LIMIT", "DROP DATABASE", "TEST_DATABASE_URL", "CI"},
		"internal/platform/database/dbtest/database_test.go": {"isolated", "connection limit", "cleanup"},
		"internal/platform/database/migrate_test.go":         {"dbtest.New(t)", ".URL().Reveal()", ".Password().Reveal()"},
	} {
		content, ok := byPath[path]
		if !ok {
			t.Errorf("generated project lacks %s", path)
			continue
		}
		for _, contract := range contracts {
			if !strings.Contains(content, contract) {
				t.Errorf("%s lacks %q", path, contract)
			}
		}
	}
}
