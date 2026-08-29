package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectInstrumentsDatabaseQueries(t *testing.T) {
	t.Parallel()
	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
	}

	contracts := map[string][]string{
		"internal/platform/database/querytrace.go": {
			"var _ pgx.QueryTracer",
			"var _ pgx.BatchTracer",
			"var _ pgx.CopyFromTracer",
			"func WithQueryBudget",
			"func NewQueryMetrics",
			"slow database query",
		},
		"internal/platform/database/querytrace_test.go": {
			"TestQueryTracerRealPostgreSQLFlow",
			"TEST_DATABASE_URL",
			"secret-query-argument",
		},
		"internal/platform/config/config.go": {
			"DB_SLOW_QUERY_THRESHOLD",
			"DatabaseSlowQueryThreshold",
		},
		"internal/app/database_runtime.go": {
			"database.NewQueryTracer",
			"database.OpenOptions{Tracer: tracer}",
		},
		"internal/app/app.go": {
			"database.NewQueryMetrics",
		},
	}
	for path, expected := range contracts {
		got, ok := content[path]
		if !ok {
			t.Errorf("generated project lacks %s", path)
			continue
		}
		for _, fragment := range expected {
			if !strings.Contains(got, fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}
}
