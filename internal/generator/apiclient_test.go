package generator_test

import (
	"strings"
	"testing"
)

// TestGeneratedAPIClientHandlesProblems pins the client's contract (Nise task
// M6-007): what it always does, and what it deliberately refuses to do.
func TestGeneratedAPIClientHandlesProblems(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	wants := map[string][]string{
		"frontend/src/lib/api/client.ts": {
			// Every request: the versioned base, the Accept both media types,
			// same-origin credentials, and forwarded cancellation.
			"const API_BASE_PATH = '/api/v1'",
			"application/json, ${problemMediaType}",
			"credentials: 'same-origin'",
			"signal: chosen.signal",
			// State changes echo the anti-forgery cookie into the header. That
			// is the whole mechanism: a form posted from another origin cannot
			// set a request header.
			"const CSRF_COOKIE_NAMES = ['__Host-session_csrf', 'session_csrf']",
			"const CSRF_HEADER = 'X-CSRF-Token'",
			"UNSAFE_METHODS.has(method)",
			// Sensitive commands can carry a key.
			"const IDEMPOTENCY_HEADER = 'Idempotency-Key'",
			// Path parameters are encoded, so an identifier holding a slash
			// cannot walk out of its segment and address another resource.
			"encodeURIComponent(String(value))",
			// A failure body becomes a validated Problem or nothing.
			"this.problem = isProblem(body) ? body : null",
		},
		"frontend/src/lib/api/problem.ts": {
			"export const problemMediaType = 'application/problem+json'",
			"export function isProblem(",
			"export function problemMessage(",
			"export function isRetryable(",
			"permissionDenied: 'permission_denied'",
			"crossSiteRequest: 'cross_site_request'",
			"cursorExpired: 'cursor_expired'",
		},
		"frontend/src/lib/components/ui/ProblemAlert.svelte": {
			"import { isRetryable, problemMessage } from '$lib/api/problem';",
			"Reference: {shown.requestID}",
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

	// Nothing in the frontend calls fetch except the client. One request
	// without the anti-forgery header, six months from now, in the one file
	// nobody re-reads, is exactly the defect this refuses.
	for path, body := range content {
		if !strings.HasPrefix(path, "frontend/src/") || strings.HasSuffix(path, ".test.ts") {
			continue
		}
		if path == "frontend/src/lib/api/client.ts" {
			continue
		}
		for _, call := range []string{"fetch(", "globalThis.fetch", "window.fetch"} {
			if strings.Contains(body, call) {
				t.Errorf("%s calls %s directly; every request goes through src/lib/api/client.ts", path, call)
			}
		}
	}
}
