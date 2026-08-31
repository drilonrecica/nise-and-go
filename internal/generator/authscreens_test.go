package generator_test

import (
	"strings"
	"testing"
)

// TestGeneratedAuthenticationSurface pins the HTTP contract the authentication
// screens are built on (Nise task M6-008), and the three properties that hold
// across all of it.
func TestGeneratedAuthenticationSurface(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	spec := content["api/openapi.yaml"]
	for _, operation := range []string{
		"operationId: signIn",
		"operationId: signOut",
		"operationId: getCurrentSession",
		"operationId: completeSecondFactor",
		"operationId: listSessions",
		"operationId: revokeSession",
		"operationId: revokeOtherSessions",
		"operationId: changePassword",
		"operationId: acceptInvitation",
	} {
		if !strings.Contains(spec, operation) {
			t.Errorf("api/openapi.yaml lacks %q", operation)
		}
	}

	// The sign-in result is discriminated by a required status rather than by
	// which optional member happens to be present, so a client cannot read an
	// unfinished sign-in as a finished one.
	for _, fragment := range []string{
		"SignInResult:",
		"- signed_in",
		"- second_factor_required",
		"      required:\n        - status",
	} {
		if !strings.Contains(spec, fragment) {
			t.Errorf("api/openapi.yaml's SignInResult lacks %q", fragment)
		}
	}

	// No response schema carries the session token. It lives in the cookie and
	// nowhere else, and a body carrying it would be logged by every proxy in
	// front of the application.
	for _, leak := range []string{"session_token", "access_token", "bearer"} {
		if strings.Contains(strings.ToLower(spec), leak) {
			t.Errorf("api/openapi.yaml mentions %q; the session credential belongs in the cookie", leak)
		}
	}

	handler := content["internal/platform/httpapi/session.go"]
	for _, fragment := range []string{
		// Both cookies move together: the anti-forgery value is derived from
		// the session token.
		"cookies.Write(w, issued.Token, issued.Record.ExpiresAt, now)",
		"cookies.WriteCSRFCookie(w, issued.Token, issued.Record.ExpiresAt, now)",
		// One public code for every reason a sign-in or an invitation failed.
		"problem.Wrap(problem.InvalidCredentials(), err)",
		"problem.Wrap(problem.InvitationInvalid(), err)",
		// The throttle is the one refusal a caller can act on.
		"problem.Wrap(problem.TooManyAttempts(), err)",
		// A session belonging to somebody else is a 404, not a 403.
		"if !ownsSession(summaries, target)",
	} {
		if !strings.Contains(handler, fragment) {
			t.Errorf("session.go lacks %q", fragment)
		}
	}

	// The strict server has to unwrap a named problem, or every refusal above
	// would answer 500.
	if !strings.Contains(content["internal/platform/httpapi/api.go"], "problem.ResponseHandler(problem.InternalServerError())") {
		t.Error("api.go does not install the problem-aware response error handler")
	}
}

// TestGeneratedAuthenticationScreens pins the frontend half: the route groups,
// the guard, and the two redirect rules that are security-relevant.
func TestGeneratedAuthenticationScreens(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	for _, path := range []string{
		"frontend/src/routes/(public)/+layout.svelte",
		"frontend/src/routes/(public)/sign-in/+page.svelte",
		"frontend/src/routes/(public)/sign-in/second-factor/+page.svelte",
		"frontend/src/routes/(public)/invitations/accept/+page.svelte",
		"frontend/src/routes/(app)/+layout.ts",
		"frontend/src/routes/(app)/account/+page.svelte",
		"frontend/src/routes/(app)/sign-out/+page.svelte",
		"frontend/src/lib/session.svelte.ts",
	} {
		if _, exists := content[path]; !exists {
			t.Errorf("generated project lacks %s", path)
		}
	}

	guard := content["frontend/src/routes/(app)/+layout.ts"]
	for _, fragment := range []string{
		"const current = await session.load(fetch);",
		"redirect(307,",
	} {
		if !strings.Contains(guard, fragment) {
			t.Errorf("the (app) layout guard lacks %q", fragment)
		}
	}

	// A `next` parameter is a redirect somebody else chooses. The
	// protocol-relative form is the one that gets missed: it starts with a
	// slash and still leaves the origin.
	sessionModule := content["frontend/src/lib/session.svelte.ts"]
	for _, fragment := range []string{
		"export function safeNext(",
		"!next.startsWith('/') || next.startsWith('//')",
	} {
		if !strings.Contains(sessionModule, fragment) {
			t.Errorf("session.svelte.ts lacks %q", fragment)
		}
	}

	// Enrollment does not hand out a session: that would make the invitation
	// token a way to obtain one without ever presenting the password just set.
	accept := content["frontend/src/routes/(public)/invitations/accept/+page.svelte"]
	if !strings.Contains(accept, "await goto(`/sign-in?email=${encodeURIComponent(accepted.email)}`)") {
		t.Error("accepting an invitation does not send the person to sign in")
	}
}

// TestGeneratedFormConventions pins the boundary the form helper exists to
// hold (Nise task M6-009): the browser validates as a courtesy, the server
// decides, and the two kinds of error never merge.
func TestGeneratedFormConventions(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	forms, exists := content["frontend/src/lib/forms.svelte.ts"]
	if !exists {
		t.Fatal("generated project lacks frontend/src/lib/forms.svelte.ts")
	}
	for _, fragment := range []string{
		"export function createForm<",
		"export const rules = {",
		"export function mustMatch<",
		// An invalid form is not sent: the server would refuse it too.
		"if (!parsed.success) {",
		// A server refusal is never attributed to a field.
		"failure = error;",
	} {
		if !strings.Contains(forms, fragment) {
			t.Errorf("forms.svelte.ts lacks %q", fragment)
		}
	}

	// The browser must not hold a password rule the server does not, or it
	// refuses passwords the server would accept — and it cannot do the breach
	// check at all.
	for _, forbidden := range []string{"[A-Z]", "\\\\d", "special character", "uppercase"} {
		if strings.Contains(forms, forbidden) {
			t.Errorf("forms.svelte.ts applies its own password composition rule (%q); the server owns the policy", forbidden)
		}
	}

	// Every form in the starter uses the convention rather than hand-rolling
	// its own state.
	for _, path := range []string{
		"frontend/src/routes/(public)/sign-in/+page.svelte",
		"frontend/src/routes/(public)/invitations/accept/+page.svelte",
	} {
		if !strings.Contains(content[path], "createForm({") {
			t.Errorf("%s does not use the shared form convention", path)
		}
	}
}
