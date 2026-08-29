package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDocumentsReviewedDirectPGXAdapter(t *testing.T) {
	t.Parallel()
	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, file := range files {
		if file.Path != "internal/platform/database/directpgx_test.go" {
			continue
		}
		content := string(file.Content)
		for _, contract := range []string{
			"type reviewedDirectPGXDB interface",
			"var _ reviewedDirectPGXDB = (pgx.Tx)(nil)",
			"pgx.CopyFromSource",
			"WHERE id = ANY($1)",
			"transactor.Within",
			"pgx.QueryTracer",
			"pgx.CopyFromTracer",
			"TestReviewedDirectPGXAdapter",
		} {
			if !strings.Contains(content, contract) {
				t.Errorf("direct pgx example lacks %q", contract)
			}
		}
		return
	}
	t.Fatal("generated project lacks reviewed direct-pgx adapter example")
}
