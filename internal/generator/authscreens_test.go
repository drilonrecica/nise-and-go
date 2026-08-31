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

// TestGeneratedListStateLivesInTheURL pins the list contract (Nise task
// M6-010): the URL is the state, one state has one URL, and the table renders
// what the server sent.
func TestGeneratedListStateLivesInTheURL(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	state, exists := content["frontend/src/lib/table/list-state.ts"]
	if !exists {
		t.Fatal("generated project lacks frontend/src/lib/table/list-state.ts")
	}
	for _, fragment := range []string{
		"export function readListState(",
		"export function listHref(",
		"export function nextSort(",
		"export function ariaSort(",
		// Paging is the only change that keeps a cursor: the API refuses one
		// presented against a different query.
		"const paging = change.after !== undefined || change.before !== undefined;",
		// One state, one URL.
		"for (const name of [...Object.keys(next.filters)].sort()) {",
	} {
		if !strings.Contains(state, fragment) {
			t.Errorf("list-state.ts lacks %q", fragment)
		}
	}

	table := content["frontend/src/lib/components/ui/DataTable.svelte"]
	for _, fragment := range []string{
		// The server sorts, filters, and pages. A table that did any of it
		// locally would make what a person sees disagree with the URL.
		"manualSorting: true",
		"manualFiltering: true",
		"manualPagination: true",
		// A sortable header is a link with a real href, and says its state.
		"aria-sort={canSort ? ariaSort(sort, header.column.id) : undefined}",
		"href={listHref(url, { sort: nextSort(sort, header.column.id) }, filters)}",
	} {
		if !strings.Contains(table, fragment) {
			t.Errorf("DataTable.svelte lacks %q", fragment)
		}
	}
}

// TestGeneratedInternationalization pins the compile-time i18n contract (Nise
// task M6-011): messages compile, the compiled output is never committed, and
// no locale reaches the URL.
func TestGeneratedInternationalization(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	for _, path := range []string{
		"frontend/project.inlang/settings.json",
		"frontend/messages/en.json",
		"frontend/src/lib/i18n.ts",
		"frontend/src/lib/components/LocaleControl.svelte",
	} {
		if _, exists := content[path]; !exists {
			t.Errorf("generated project lacks %s", path)
		}
	}

	// The compiled directory is generated output. Committing it would make a
	// message change a two-file change, and the second file the one nobody
	// regenerates.
	if !strings.Contains(content["frontend/.gitignore"], "src/lib/paraglide/") {
		t.Error("frontend/.gitignore does not ignore the compiled messages")
	}

	pkg := content["frontend/package.json"]
	for _, fragment := range []string{
		`"messages:compile": "paraglide-js compile`,
		"pnpm run messages:compile &&",
	} {
		if !strings.Contains(pkg, fragment) {
			t.Errorf("frontend/package.json lacks %q", fragment)
		}
	}

	// No locale segment in the URL: one application, one path, and a route
	// that does not exist once per language.
	vite := content["frontend/vite.config.ts"]
	if !strings.Contains(vite, "strategy: ['localStorage', 'preferredLanguage', 'baseLocale']") {
		t.Error("vite.config.ts does not pin the non-URL locale strategy")
	}
	if strings.Contains(vite, "'url'") {
		t.Error("vite.config.ts enables the URL locale strategy")
	}
}

// TestGeneratedInterfaceStates pins the five states every surface has to be
// able to be in (Nise task M6-012), and the one distinction that keeps them
// useful: they ask a person to do different things.
func TestGeneratedInterfaceStates(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	// Loading, empty, and error each have a component; denied and not-found
	// are statuses the one error boundary distinguishes.
	for _, path := range []string{
		"frontend/src/lib/components/ui/Skeleton.svelte",
		"frontend/src/lib/components/ui/EmptyState.svelte",
		"frontend/src/lib/components/ui/ProblemAlert.svelte",
		"frontend/src/routes/+error.svelte",
	} {
		if _, exists := content[path]; !exists {
			t.Errorf("generated project lacks %s", path)
		}
	}

	boundary := content["frontend/src/routes/+error.svelte"]
	for _, status := range []string{"403:", "404:", "401:"} {
		if !strings.Contains(boundary, status) {
			t.Errorf("+error.svelte does not distinguish status %s; sign in, ask for access, and go back are three different instructions", status)
		}
	}

	// The account page renders all of loading, empty, error, and content, so
	// the starter demonstrates the convention rather than only documenting it.
	account := content["frontend/src/routes/(app)/account/+page.svelte"]
	for _, fragment := range []string{
		"<Skeleton",
		"<EmptyState",
		"<ProblemAlert",
		"aria-busy=\"true\"",
	} {
		if !strings.Contains(account, fragment) {
			t.Errorf("the account page lacks %q", fragment)
		}
	}
}

// TestGeneratedAboutPageIsPermissionedAndRemovable pins both halves of the
// branding rule (Nise task M6-013): the version information is behind a
// permission, and the framework reference is one paragraph an application can
// delete.
func TestGeneratedAboutPageIsPermissionedAndRemovable(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	// The permission exists, is in the catalog, and belongs to the
	// administrator bundle only.
	catalog := content["internal/platform/authorization/catalog.go"]
	for _, fragment := range []string{
		`SystemRead = authz.MustPermission("system.read")`,
		"\t\tSystemRead,\n",
	} {
		if !strings.Contains(catalog, fragment) {
			t.Errorf("the permission catalog lacks %q", fragment)
		}
	}
	if strings.Contains(catalog, "authz.NewRole(RoleAuditor, UsersRead, RolesRead, AuditRead, SystemRead)") {
		t.Error("the auditor role holds system.read; the deployment's version is not part of looking at the business")
	}

	// The handler checks it, and checks for a session first.
	system := content["internal/platform/httpapi/system.go"]
	for _, fragment := range []string{
		"if _, err := requireSession(ctx); err != nil {",
		"authorization.Require(ctx, authorization.SystemRead)",
		"problem.Wrap(problem.PermissionDenied(), err)",
		// Read from the build rather than from a linker flag, so the answer
		// is true for `go build`, `go install`, and a release alike.
		"debug.ReadBuildInfo()",
	} {
		if !strings.Contains(system, fragment) {
			t.Errorf("system.go lacks %q", fragment)
		}
	}

	// The framework reference appears exactly once in the generated frontend,
	// on that page, and says it may be deleted.
	mentions := 0
	for path, body := range content {
		if !strings.HasPrefix(path, "frontend/src/") {
			continue
		}
		if strings.Contains(body, "Built with Nise") {
			mentions++
			if path != "frontend/src/routes/(app)/settings/about/+page.svelte" {
				t.Errorf("%s carries a framework reference; it belongs only on the admin About page", path)
			}
			if !strings.Contains(body, "delete it freely") {
				t.Errorf("%s does not say the reference may be removed", path)
			}
		}
	}
	if mentions != 1 {
		t.Errorf("the framework is mentioned in %d generated frontend files, want exactly 1", mentions)
	}
}

// TestGeneratedComponentTestFoundations pins the two-suite split and what the
// browser half is for (Nise task M6-014).
func TestGeneratedComponentTestFoundations(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	config := content["frontend/vitest.config.ts"]
	for _, fragment := range []string{
		"name: 'unit'",
		"name: 'browser'",
		"provider: playwright()",
		"include: ['src/**/*.browser.test.ts']",
		// A component rendered without the stylesheet has every size,
		// spacing, and colour class inert, so an accessibility assertion
		// about a target's size measures unstyled markup.
		"setupFiles: ['./src/browser-setup.ts']",
		// The Node suite must not pick up a browser test, or it fails on the
		// first thing that needs a real browser.
		"exclude: ['src/**/*.browser.test.ts']",
	} {
		if !strings.Contains(config, fragment) {
			t.Errorf("vitest.config.ts lacks %q", fragment)
		}
	}

	a11y := content["frontend/src/lib/a11y.ts"]
	for _, tag := range []string{"wcag2aa", "wcag21aa", "wcag22aa"} {
		if !strings.Contains(a11y, tag) {
			t.Errorf("a11y.ts does not check the %s rule set", tag)
		}
	}
	// A clean axe run is a floor, and the file has to say so: roughly a third
	// of WCAG is not machine-checkable at all.
	if !strings.Contains(a11y, "floor, never a certificate") {
		t.Error("a11y.ts does not state that a clean automated run is not a certificate")
	}

	pkg := content["frontend/package.json"]
	for _, fragment := range []string{
		`"test:unit": "pnpm run messages:compile && svelte-kit sync && vitest run --project unit"`,
		`"test:browser": "pnpm run messages:compile && svelte-kit sync && vitest run --project browser"`,
		`"test:component": "pnpm run messages:compile && svelte-kit sync && vitest run"`,
		`"test:browser:install": "playwright install chromium"`,
	} {
		if !strings.Contains(pkg, fragment) {
			t.Errorf("frontend/package.json lacks %q", fragment)
		}
	}
}

// TestGeneratedEndToEndFoundation pins what the end-to-end suite is for and
// what keeps it usable (Nise task M6-015).
func TestGeneratedEndToEndFoundation(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	config, exists := content["frontend/playwright.config.ts"]
	if !exists {
		t.Fatal("generated project lacks frontend/playwright.config.ts")
	}
	for _, fragment := range []string{
		"testDir: './e2e'",
		// Journeys share one database, so they run one at a time.
		"workers: 1",
		"fullyParallel: false",
		// A trace of the first retry is what makes a CI failure readable
		// hours later.
		"trace: 'on-first-retry'",
		// Point it at a running instance, or let it build and start one.
		"process.env.APP_BASE_URL",
		"healthz/ready",
	} {
		if !strings.Contains(config, fragment) {
			t.Errorf("playwright.config.ts lacks %q", fragment)
		}
	}

	// The authenticated journey needs an account, which is deployment-specific,
	// so it skips with a reason rather than inventing a way to create one from
	// outside the application.
	session := content["frontend/e2e/session.spec.ts"]
	for _, fragment := range []string{
		"process.env.E2E_EMAIL",
		"test.skip(",
		// The credential is in a cookie the page cannot read.
		"expect(readable).not.toContain('session=')",
	} {
		if !strings.Contains(session, fragment) {
			t.Errorf("e2e/session.spec.ts lacks %q", fragment)
		}
	}

	// The suite is opt-in: it needs a database and takes minutes, so it is not
	// part of the checks that run on every change.
	if strings.Contains(content["frontend/package.json"], `"check": "pnpm run api:check && pnpm run messages:compile && pnpm run check:runes && pnpm run check:contrast && svelte-kit sync && svelte-check --tsconfig ./tsconfig.json && playwright`) {
		t.Error("pnpm check runs the end-to-end suite; it needs a database and belongs behind nise test --e2e")
	}
	if !strings.Contains(content["frontend/package.json"], `"test:e2e": "playwright test"`) {
		t.Error("frontend/package.json does not declare test:e2e, which nise test --e2e looks for")
	}
}
