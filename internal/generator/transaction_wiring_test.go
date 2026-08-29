package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectWiresUseCaseOwnedTransactions(t *testing.T) {
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
		"internal/platform/database/transaction.go":      {"NewTransactor", "pgx.TxOptions", "transaction.Options"},
		"internal/platform/database/transaction_test.go": {"sqlcStyleDBTX", "transactor.Within", "newSQLCStyleQueries(tx)"},
		"internal/app/database_runtime.go":               {"database.NewTransactor", "databaseRuntime"},
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
