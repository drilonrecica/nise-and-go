package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDefinesVersionedAPIRouterCore(t *testing.T) {
	t.Parallel()
	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
	}

	for _, fragment := range []string{
		"type MiddlewareSlots struct",
		"RegisterAPI func(chi.Router)",
		"secure.NewDocumentPolicy",
		"secure.NewAPIPolicy",
		`root.Mount("/api/v1", newCoreHandler(d, apiPolicy, d.Middleware.API, api))`,
		"newCoreHandler",
	} {
		if !strings.Contains(content["internal/platform/httpapi/router.go"], fragment) {
			t.Errorf("router.go lacks %q", fragment)
		}
	}
	for _, fragment := range []string{
		"TestRouterUsesIndependentAPIAndDocumentPolicies",
		"TestRouterCoreWrapsApplicationMiddleware",
		"TestRouterKeepsUnmatchedAPIPathsOutOfTheUI",
	} {
		if !strings.Contains(content["internal/platform/httpapi/router_test.go"], fragment) {
			t.Errorf("router_test.go lacks %q", fragment)
		}
	}
}
