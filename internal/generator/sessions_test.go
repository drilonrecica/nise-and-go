package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDefinesOpaqueSessions(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	owners := make(map[string]generator.Ownership, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
		owners[file.Path] = file.Owner
	}

	wants := map[string][]string{
		"db/migrations/00003_identity.sql": {
			"CREATE TABLE users",
			"CREATE TABLE sessions",
			"token_digest   bytea NOT NULL",
			"CONSTRAINT sessions_token_digest_length CHECK (octet_length(token_digest) = 32)",
			"CONSTRAINT sessions_lifetime CHECK (expires_at > created_at)",
			"CONSTRAINT users_email_normalized CHECK (email = lower(email))",
			"CREATE UNIQUE INDEX sessions_token_digest_key",
			"CREATE UNIQUE INDEX users_email_key",
			"-- +goose Down",
		},
		"internal/features/auth/sqlc.yaml": {
			"schema: ../../../db/migrations",
			"out: store",
			"nise-no-unbounded-delete",
			"nise-no-unbounded-update",
			"nise-no-truncate",
		},
		"internal/features/auth/queries/sessions.sql": {
			"-- name: CreateSession :one",
			"-- name: FindSessionByTokenDigest :one",
			"-- name: TouchSession :execrows",
			"-- name: RevokeSession :execrows",
			"-- name: DeleteExpiredSessions :execrows",
			"u.status AS user_status",
		},
		"internal/features/auth/store/querier.go": {
			generator.SQLCGeneratedHeader,
			"sqlc " + generator.SQLCVersion,
			"type Querier interface",
			"CreateSession(ctx context.Context",
		},
		"internal/features/auth/sessions.go": {
			`"github.com/drilonrecica/nise-and-go/runtime/session"`,
			"func (s *Sessions) Issue(ctx context.Context, userID string) (Issued, error)",
			"func (s *Sessions) Authenticate(ctx context.Context, presented string) (session.Record, error)",
			"s.transactor.Within(ctx, transaction.Options{}",
			"TokenDigest: token.Digest()",
			"s.lifetime.NeedsTouch(found.LastSeenAt, now)",
			`row.UserStatus != "active"`,
		},
		"internal/features/auth/accounts.go": {
			"func NormalizeEmail(email string) (string, error)",
			"func (a *Accounts) FindCredentialByEmail(",
			"func (a *Accounts) SetStatus(",
			`pgErr.SQLState() == "23505"`,
		},
		"internal/features/auth/auth_test.go": {
			"dbtest.New(t)",
			"TestSessionIssueStoresOnlyTheDigest",
			"TestAuthenticateEnforcesBothClocks",
			"TestAuthenticateRespectsTheAbsoluteDeadline",
			"TestTouchIsBoundedByTheTouchInterval",
			"TestDisabledAccountStopsAuthenticating",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The token itself must not appear in any query or in the use case's
	// storage calls: only its digest crosses that boundary.
	for _, path := range []string{
		"internal/features/auth/queries/sessions.sql",
		"internal/features/auth/store/sessions.sql.go",
	} {
		if strings.Contains(content[path], "token_value") || strings.Contains(content[path], "TokenValue") {
			t.Errorf("%s names a stored token value", path)
		}
	}

	// queries/ is the application's; store/ is sqlc's. The directory names
	// are the ownership declaration, so neither carries a header comment.
	if owners["internal/features/auth/queries/sessions.sql"] != generator.OwnerApp {
		t.Error("a feature's hand-written SQL is not application-owned")
	}
	if owners["internal/features/auth/store/querier.go"] != generator.OwnerNise {
		t.Error("a feature's sqlc output is not tool-owned")
	}
	if strings.Contains(content["internal/features/auth/queries/sessions.sql"], generator.AppOwnedHeader) {
		t.Error("a query file carries an ownership header; sqlc would copy it into the generated doc comment")
	}
	for _, path := range []string{
		"internal/features/auth/queries/sessions.sql",
		"internal/features/auth/queries/users.sql",
	} {
		if !generator.IsFeatureQueryPath(path) {
			t.Errorf("%s is not recognized as a feature query path", path)
		}
	}
	for _, path := range []string{
		"internal/features/auth/store/db.go",
		"internal/features/auth/store/models.go",
		"internal/features/auth/store/sessions.sql.go",
	} {
		if !generator.IsSQLCGeneratedPath(path) {
			t.Errorf("%s is not recognized as sqlc output", path)
		}
	}
	for _, path := range []string{
		"internal/features/auth/sessions.go",
		"internal/features/auth/queries/sessions.sql",
		"internal/platform/idempotency/idempotency.go",
		"store/db.go",
	} {
		if generator.IsSQLCGeneratedPath(path) && !strings.Contains(path, "/store/") {
			t.Errorf("%s was misclassified as sqlc output", path)
		}
	}
}

// TestGeneratedProjectDocumentsTheCookieDecision keeps the cookie policy's one
// hard rule visible where a reader of the generated project will meet it: the
// prefix and Secure move together, and development is not a weaker branch.
func TestGeneratedProjectDocumentsTheCookieDecision(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
	}
	for path, fragments := range map[string][]string{
		"README.md": {
			"__Host-",
			"SESSION_COOKIE_INSECURE",
		},
		".env.example": {
			"SESSION_COOKIE_INSECURE=false",
		},
	} {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}
}

func TestGeneratedProjectCarriesTheSessionCookie(t *testing.T) {
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
		"internal/platform/httpauth/cookie.go": {
			"func (c *Cookies) Write(w http.ResponseWriter, token session.Token, expiresAt time.Time, now time.Time)",
			"func (c *Cookies) Clear(w http.ResponseWriter)",
			"func (c *Cookies) Read(r *http.Request) (string, bool)",
			"HttpOnly: true",
			"Secure:   c.policy.Secure()",
			"SameSite: c.policy.SameSite()",
			"r.CookiesNamed(c.policy.Name())",
		},
		"internal/platform/httpauth/context.go": {
			"type contextKey struct{}",
			"func WithSession(ctx context.Context, record session.Record) context.Context",
			"func FromContext(ctx context.Context) (session.Record, bool)",
		},
		"internal/platform/httpauth/middleware.go": {
			"type Authenticator interface",
			"func (s *Resolver) Middleware(next http.Handler) http.Handler",
			"s.cookies.Clear(w)",
			"SessionRefusalReason() string",
		},
		"internal/platform/httpauth/httpauth_test.go": {
			"TestWriteSetsEveryHardenedAttribute",
			"TestClearMatchesWhatWriteSent",
			"TestReadRequiresExactlyOneCandidate",
			"TestResolverClearsACredentialThatDidNotResolve",
			"TestResolverNeverLogsTheCredential",
			"TestContextIdentityCannotBeForged",
		},
		"internal/platform/config/config.go": {
			"SessionLifetime session.Lifetime",
			"SessionCookie session.CookiePolicy",
			`l.Duration("SESSION_IDLE_TIMEOUT"`,
			`l.Duration("SESSION_ABSOLUTE_TIMEOUT"`,
			`l.Duration("SESSION_TOUCH_INTERVAL"`,
			`l.Bool("SESSION_COOKIE_INSECURE"`,
			`v.Check(cfg.SessionCookie.Hardened(), "SESSION_COOKIE_INSECURE"`,
		},
		"internal/app/app.go": {
			"auth.NewSessions(transactor, cfg.SessionLifetime)",
			"httpauth.NewCookies(cfg.SessionCookie)",
			"httpauth.NewResolver(sessions, sessionCookies)",
			"API:      []httpapi.Middleware{sessionResolver.Middleware}",
			"Document: []httpapi.Middleware{sessionResolver.Middleware}",
		},
		".env.example": {
			"SESSION_IDLE_TIMEOUT=12h",
			"SESSION_ABSOLUTE_TIMEOUT=720h",
			"SESSION_TOUCH_INTERVAL=5m",
			"SESSION_COOKIE_NAME=",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The resolver must not decide anything. A status write in it would be an
	// authorization control no handler's reader can see.
	middleware := content["internal/platform/httpauth/middleware.go"]
	for _, forbidden := range []string{"WriteHeader", "StatusUnauthorized", "StatusForbidden", "http.Error("} {
		if strings.Contains(middleware, forbidden) {
			t.Errorf("the session resolver rejects requests: it contains %q", forbidden)
		}
	}
}
