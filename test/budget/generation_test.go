package budget_test

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// What a generated project weighs.
//
// This is a budget rather than a measurement because a generated project is
// something a person reads. Every file in it is one they may have to
// understand before they can change anything, and a framework that adds two
// hundred of them without anybody noticing has changed what it is — from
// something you own to something you host.
//
// The numbers are deliberately close to the current ones. A budget with room
// for a doubling is not a budget; it is a note about the past.
const (
	// maxDefaultFiles and maxAllModulesFiles bound the file count.
	maxDefaultFiles    = 250
	maxAllModulesFiles = 312

	// maxDefaultBytes and maxAllModulesBytes bound the total size. They are
	// generous relative to the counts above, because prose is what most of
	// the growth is and prose is the part worth having.
	maxDefaultBytes    = 1_540_000
	maxAllModulesBytes = 1_980_000

	// maxSingleFileBytes bounds any one generated file. A file over this is
	// one nobody reads, which for application-owned code means a file
	// nobody will change either.
	maxSingleFileBytes = 60_000
)

func plan(t *testing.T, modules []recipe.Module) []generator.File {
	t.Helper()

	files, err := generator.Plan(generator.Options{
		Name:       "budget",
		ModulePath: "example.com/budget",
		CLIVersion: "v0.0.0-budget",
		Modules:    modules,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return files
}

func measure(files []generator.File) (count int, total int) {
	for _, file := range files {
		count++
		total += len(file.Content)
	}
	return count, total
}

// TestGeneratedProjectSizeBudget gates what a person receives.
func TestGeneratedProjectSizeBudget(t *testing.T) {
	t.Parallel()

	for _, variant := range []struct {
		name     string
		modules  []recipe.Module
		maxFiles int
		maxBytes int
	}{
		{"default", nil, maxDefaultFiles, maxDefaultBytes},
		{"all-modules", recipe.Modules(), maxAllModulesFiles, maxAllModulesBytes},
	} {
		t.Run(variant.name, func(t *testing.T) {
			files := plan(t, variant.modules)
			count, total := measure(files)

			if count > variant.maxFiles {
				t.Errorf("a %s project has %d files, budget is %d.\n"+
					"Every one of these is a file somebody may have to read before they can change anything.",
					variant.name, count, variant.maxFiles)
			}
			if total > variant.maxBytes {
				t.Errorf("a %s project is %d bytes, budget is %d", variant.name, total, variant.maxBytes)
			}
			t.Logf("%s: %d files, %d bytes (budgets %d, %d)", variant.name, count, total, variant.maxFiles, variant.maxBytes)
		})
	}
}

// TestNoGeneratedFileIsTooLargeToRead bounds any single file.
//
// The limit is about reading, not about disk. An application-owned file
// nobody will read is one nobody will change, which makes "you own this code"
// untrue in the only sense that matters.
func TestNoGeneratedFileIsTooLargeToRead(t *testing.T) {
	t.Parallel()

	var oversized []string
	for _, file := range plan(t, recipe.Modules()) {
		if len(file.Content) > maxSingleFileBytes {
			oversized = append(oversized, fmt.Sprintf("%s (%d bytes)", file.Path, len(file.Content)))
		}
	}
	sort.Strings(oversized)
	if len(oversized) != 0 {
		t.Errorf("these generated files are larger than %d bytes:\n  %s\n"+
			"Split them, or raise the budget deliberately and say why a file that size is worth shipping.",
			maxSingleFileBytes, strings.Join(oversized, "\n  "))
	}
}

// TestAModuleCostsWhatItIsWorth reports what selecting each module adds.
//
// It gates nothing — the right size for a module is a judgement, not a number
// — but it puts the figure in the test log, so a module that quietly triples
// is visible in a CI run rather than only in a diff nobody sized.
func TestAModuleCostsWhatItIsWorth(t *testing.T) {
	t.Parallel()

	baseCount, baseBytes := measure(plan(t, nil))
	for _, module := range recipe.Modules() {
		count, bytes := measure(plan(t, []recipe.Module{module}))
		t.Logf("%s adds %d files and %d bytes", module, count-baseCount, bytes-baseBytes)
	}
}

// TestGeneratedTreeHasNoUnexpectedTopLevelEntries is a shape budget rather
// than a size one.
//
// A generated project's root is the first thing anybody sees, and every entry
// in it is a claim about what the project is. Adding one should be a
// deliberate act with a line in this test, not something that arrives with a
// feature.
func TestGeneratedTreeHasNoUnexpectedTopLevelEntries(t *testing.T) {
	t.Parallel()

	expected := map[string]bool{
		".dockerignore": true,
		".env.example":  true,
		".gitignore":    true,
		".golangci.yml": true,
		".nise":         true,
		"AGENTS.md":     true,
		"Makefile":      true,
		"README.md":     true,
		"api":           true,
		"cmd":           true,
		"db":            true,
		"deploy":        true,
		"frontend":      true,
		"go.mod":        true,
		"internal":      true,
		"nise.json":     true,
	}

	seen := map[string]bool{}
	for _, file := range plan(t, recipe.Modules()) {
		top := file.Path
		if index := strings.Index(top, "/"); index >= 0 {
			top = top[:index]
		}
		seen[top] = true
	}

	for entry := range seen {
		if !expected[entry] {
			t.Errorf("a generated project's root contains %q, which this budget does not expect", entry)
		}
	}
	for entry := range expected {
		if !seen[entry] {
			t.Errorf("a generated project's root no longer contains %q", entry)
		}
	}
	_ = path.Clean("")
}
