package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDefinesRFC9457ProblemDetails(t *testing.T) {
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
		"api/openapi.yaml": {
			"Problem:",
			"application/problem+json:",
			"additionalProperties: false",
			"pattern: '^([A-Za-z][A-Za-z0-9+.-]*:|/$|/[^/])'",
			"request_id:",
			"correlation_id:",
		},
		"internal/platform/httpapi/openapigen/openapi.gen.go": {
			"type Problem struct",
			`json:"request_id"`,
			`json:"correlation_id"`,
		},
		"frontend/src/lib/api/schema.d.ts": {
			"Problem:",
			"request_id: string;",
			"correlation_id: string;",
		},
		"internal/platform/httpapi/httpjson/json.go": {
			"func WriteMediaType(",
		},
		"internal/platform/httpapi/problem/problem.go": {
			"application/problem+json",
			"type Definition struct",
			"InvalidRequest",
			"InternalServerError",
			"openapigen.Problem",
			"logging.RequestID",
		},
		"internal/platform/httpapi/problem/problem_test.go": {
			"TestWriteRFC9457Problem",
			"TestDefinitionValidation",
			"TestHandlerKeepsCausePrivate",
		},
		"internal/platform/httpapi/api.go": {
			"problem.Handler",
			"problem.RequestBodyTooLarge()",
			"problem.UnsupportedMediaType()",
		},
		"internal/platform/httpapi/router.go": {
			"api.NotFound(problem.HTTPHandler(problem.NotFound()))",
			"api.MethodNotAllowed(apiMethodNotAllowed(api))",
			"problem.HTTPHandler(problem.MethodNotAllowed())",
			"problem.HTTPHandler(problem.InternalServerError())",
		},
	}

	for path, fragments := range wants {
		body, exists := content[path]
		if !exists {
			t.Errorf("generated project lacks %s", path)
			continue
		}
		for _, fragment := range fragments {
			if !strings.Contains(body, fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}
	if got := strings.Count(content["api/openapi.yaml"], "minLength: 1"); got < 5 {
		t.Errorf("api/openapi.yaml has %d minLength constraints, want at least five for nonempty Problem text and IDs", got)
	}
}
