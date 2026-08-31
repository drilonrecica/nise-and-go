package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectKeepsRegistrationClosed(t *testing.T) {
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
		"db/migrations/00006_invitations.sql": {
			"CREATE TABLE invitations",
			"CONSTRAINT invitations_token_digest_length CHECK (octet_length(token_digest) = 32)",
			"CONSTRAINT invitations_terminal_state CHECK (accepted_at IS NULL OR revoked_at IS NULL)",
			"CREATE UNIQUE INDEX invitations_open_email_key ON invitations (email)",
			"-- +goose Down",
		},
		"internal/features/auth/queries/invitations.sql": {
			"FOR UPDATE",
			"-- name: MarkInvitationAccepted :execrows",
			"AND accepted_at IS NULL",
			"-- name: RevokeAllOpenInvitations :execrows",
			"-- name: CountUsers :one",
		},
		"internal/features/auth/invitations.go": {
			`InvitationTokenPrefix = "inv1"`,
			"func (i *Invitations) Accept(ctx context.Context, rawToken, secret string) (Account, error)",
			"func (i *Invitations) Bootstrap(ctx context.Context, email string) (Invitation, InvitationToken, error)",
			"authorization.Require(ctx, authorization.UsersManage)",
			"return ErrDirectoryNotEmpty",
			"queries.RevokeAllOpenInvitations(ctx,",
		},
		"internal/app/enroll.go": {
			"func RunBootstrap(ctx context.Context, email string) (BootstrapResult, error)",
			"invitations.Bootstrap(ctx, email)",
		},
		"cmd/myapp/main.go": {
			`args[0] == "admin"`,
			"func parseAdminArgs(args []string) (email string, jsonOutput bool, err error)",
		},
		"internal/features/auth/invitations_test.go": {
			"TestOneInvitationCreatesAtMostOneAccount",
			"TestEveryUnusableInvitationLooksTheSame",
			"TestBootstrapOnlyWorksOnAnEmptyDirectory",
			"TestRerunningTheBootstrapInvalidatesTheEarlierLink",
			"TestEnrollmentIsClosedByDefault",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The invitation token must not share the session token's prefix: a
	// credential presentable in two places is one that will be.
	invitations := content["internal/features/auth/invitations.go"]
	if strings.Contains(invitations, `"ns1"`) || strings.Contains(invitations, "session.New()") {
		t.Error("the invitation token reuses the session token")
	}

	// Acceptance must lock the invitation, or two people following one link
	// both create an account.
	if !strings.Contains(content["internal/features/auth/queries/invitations.sql"], "FOR UPDATE") {
		t.Error("the invitation lookup does not lock the row")
	}

	// There must be no self-service registration: every write that creates an
	// account is either an acceptance or the bootstrap.
	for _, forbidden := range []string{"func (i *Invitations) Register(", "SignUp", "signup"} {
		if strings.Contains(invitations, forbidden) {
			t.Errorf("the enrollment feature contains a self-service path: %q", forbidden)
		}
	}
}
