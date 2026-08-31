package generator_test

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectRequiresRecentProofForSensitiveActions(t *testing.T) {
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
		"db/migrations/00008_reauthentication.sql": {
			"ALTER TABLE sessions ADD COLUMN proven_at timestamptz",
			"UPDATE sessions SET proven_at = created_at WHERE proven_at IS NULL",
			"ALTER COLUMN proven_at SET NOT NULL",
			"CHECK (proven_at >= created_at)",
			"-- +goose Down",
		},
		"internal/platform/reauth/matrix.go": {
			"func NewMatrix(entries ...Entry) (*Matrix, error)",
			"func DefaultMatrix() (*Matrix, error)",
			`ActionRolesGrant Action = "roles.grant"`,
			`ActionUsersEnable Action = "users.enable"`,
			`ActionInvitationsCreate Action = "invitations.create"`,
		},
		"internal/platform/reauth/require.go": {
			"func Require(ctx context.Context, action Action) error",
			"if !ok || state.At.IsZero() {",
			"freshness.Satisfied(state.ProvenAt, state.At)",
			"func (r *Resolver) Middleware(next http.Handler) http.Handler",
		},
		"internal/features/auth/queries/sessions.sql": {
			"SELECT previous.user_id, $2, previous.created_at, $3, previous.expires_at, previous.proven_at",
			"-- name: StampSessionProof :execrows",
			"AND proven_at <= $2",
		},
		"internal/features/auth/sessions.go": {
			"func (s *Sessions) RecordProof(ctx context.Context, sessionID string) (time.Time, error)",
			"ProvenAt:   provenAt.Time.UTC(),",
		},
		"internal/features/auth/login.go": {
			"func (c *Credentials) Reauthenticate(ctx context.Context, secret string) (time.Time, error)",
			`ActionReauthenticated = "session.reauthenticated"`,
			`ActionReauthenticationDenied = "session.reauthentication_denied"`,
			"c.throttle.Allow(ctx, account.Email, forwarded.ClientIP(ctx))",
		},
		"internal/features/auth/reauthentication_test.go": {
			"TestReauthenticationProvesOneSessionAndNoOther",
			"TestActionsInTheMatrixNeedARecentProof",
			"TestWithdrawingAccessIsNotSlowedDown",
			"TestReauthenticationSharesTheSignInThrottle",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// A session is proved when it is issued, and rotation carries the proof
	// across. Issuing without one would ask everybody to reauthenticate the
	// instant they signed in; losing it on rotation would do the same after a
	// password change.
	queries := content["internal/features/auth/queries/sessions.sql"]
	if !strings.Contains(queries, "INSERT INTO sessions (user_id, token_digest, created_at, last_seen_at, expires_at, proven_at)\nVALUES ($1, $2, $3, $3, $4, $3)") {
		t.Error("issuing a session does not record the proof it was issued with")
	}

	// The proof is a session column, not an account column. Proving yourself
	// in one browser must not make a session on another machine fresh.
	migration := content["db/migrations/00008_reauthentication.sql"]
	if strings.Contains(migration, "ALTER TABLE users") {
		t.Error("the proof of identity is stored on the account rather than the session")
	}

	// Reauthentication must not extend a session. Recording a proof that also
	// slid the absolute deadline would make hourly reauthentication a session
	// that never ends.
	stamp := queries[strings.Index(queries, "-- name: StampSessionProof"):]
	for _, forbidden := range []string{"expires_at", "last_seen_at"} {
		if strings.Contains(stamp, forbidden) {
			t.Errorf("recording a proof also writes %s", forbidden)
		}
	}
}

// Every action the matrix declares is checked by a use case, and every action a
// use case checks is declared. The two are one commit, and a check naming an
// action nobody declared would deny forever while looking like a control.
func TestGeneratedMatrixAndItsChecksAgree(t *testing.T) {
	t.Parallel()

	for _, variant := range []struct {
		name    string
		options generator.Options
		want    []string
	}{
		{
			name:    "without modules",
			options: defaultOptions(),
			want:    []string{"invitations.create", "roles.grant", "users.enable"},
		},
		{
			// A module may extend the matrix, on the same principle:
			// removing a second factor widens what a stolen session can do.
			name:    "with every module",
			options: allModulesOptions(),
			want:    []string{"invitations.create", "roles.grant", "second_factor.disable", "users.enable"},
		},
	} {
		t.Run(variant.name, func(t *testing.T) {
			t.Parallel()
			assertMatrixAgrees(t, variant.options, variant.want)
		})
	}
}

func assertMatrixAgrees(t *testing.T, options generator.Options, want []string) {
	t.Helper()

	files, err := generator.Plan(options)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	declaration := regexp.MustCompile(`(?m)^\tAction([A-Za-z]+) Action = "([a-z0-9_.]+)"$`)
	check := regexp.MustCompile(`reauth\.Require\(ctx, reauth\.Action([A-Za-z]+)\)`)

	declared := map[string]string{}
	checked := map[string]bool{}
	for _, file := range files {
		body := string(file.Content)
		if file.Path == "internal/platform/reauth/matrix.go" {
			for _, match := range declaration.FindAllStringSubmatch(body, -1) {
				declared[match[1]] = match[2]
			}
			continue
		}
		if !strings.HasSuffix(file.Path, ".go") || strings.HasSuffix(file.Path, "_test.go") {
			continue
		}
		for _, match := range check.FindAllStringSubmatch(body, -1) {
			checked[match[1]] = true
		}
	}

	if len(declared) == 0 {
		t.Fatal("the matrix declares no actions")
	}
	for name, action := range declared {
		if !checked[name] {
			t.Errorf("the matrix declares %s and no use case checks it", action)
		}
	}
	for name := range checked {
		if _, ok := declared[name]; !ok {
			t.Errorf("a use case checks Action%s, which the matrix does not declare", name)
		}
	}

	names := make([]string, 0, len(declared))
	for _, action := range declared {
		names = append(names, action)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("the matrix declares %v, want %v; changing it is an ADR-sized decision", names, want)
	}
}

// The proof check runs after the permission check, and the position is resolved
// after the session it belongs to.
func TestGeneratedProofChecksRunInTheRightOrder(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
	}

	for _, path := range []string{
		"internal/features/auth/roles.go",
		"internal/features/auth/accounts.go",
		"internal/features/auth/invitations.go",
	} {
		body := content[path]
		permission := strings.Index(body, "authorization.Require(ctx,")
		proof := strings.Index(body, "reauth.Require(ctx,")
		if permission < 0 || proof < 0 {
			t.Errorf("%s does not check both a permission and a proof", path)
			continue
		}
		if permission > proof {
			t.Errorf("%s asks whether it is still you before asking whether you may", path)
		}
	}

	// Withdrawing access must not be gated on a proof: the withdrawal is the
	// response to a compromise.
	roles := content["internal/features/auth/roles.go"]
	revoke := roles[strings.Index(roles, "func (r *Roles) Revoke("):]
	if strings.Contains(revoke[:strings.Index(revoke, "\n}\n")], "reauth.Require(") {
		t.Error("revoking a role requires a recent proof")
	}
	accounts := content["internal/features/auth/accounts.go"]
	if !strings.Contains(accounts, "if status == StatusActive {") {
		t.Error("disabling an account is gated on a proof as well as enabling one")
	}

	// The position is resolved from the same session authority is, and before
	// the anti-forgery guard, so one request answers both questions once.
	app := content["internal/app/app.go"]
	sessionResolver := strings.Index(app, "sessionResolver.Middleware, authorizationResolver.Middleware, reauthResolver.Middleware")
	if sessionResolver < 0 {
		t.Error("the reauthentication position is not resolved after the session and the authority")
	}
	if !strings.Contains(app, "reauth.NewResolver(reauthMatrix, httpauth.FromContext)") {
		t.Error("the reauthentication resolver does not read the resolved session")
	}
}
