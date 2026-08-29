package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectOwnsReadableEmbeddedMigrations(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	byPath := make(map[string]generator.File, len(files))
	for _, file := range files {
		byPath[file.Path] = file
	}

	baseline, ok := byPath["db/migrations/00001_baseline.sql"]
	if !ok {
		t.Fatal("generated project has no readable baseline migration")
	}
	if baseline.Owner != generator.OwnerApp {
		t.Errorf("baseline migration owner = %q, want %q", baseline.Owner, generator.OwnerApp)
	}
	for _, marker := range []string{"-- +goose Up", "-- +goose Down"} {
		if !strings.Contains(string(baseline.Content), marker) {
			t.Errorf("baseline migration lacks %q", marker)
		}
	}

	embed, ok := byPath["db/embed.gen.go"]
	if !ok {
		t.Fatal("generated project has no migration embed package")
	}
	if embed.Owner != generator.OwnerNise {
		t.Errorf("migration embed owner = %q, want %q", embed.Owner, generator.OwnerNise)
	}
	for _, contract := range []string{"//go:embed migrations", "fs.Sub", "func Migrations() fs.FS"} {
		if !strings.Contains(string(embed.Content), contract) {
			t.Errorf("migration embed source lacks %q", contract)
		}
	}
}
