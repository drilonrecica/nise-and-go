package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDeclaresAClosedPermissionCatalog(t *testing.T) {
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
		"db/migrations/00005_authorization.sql": {
			"CREATE TABLE user_roles",
			"CONSTRAINT user_roles_pkey PRIMARY KEY (user_id, role)",
			"REFERENCES users (id) ON DELETE CASCADE",
			"-- +goose Down",
		},
		"internal/platform/authorization/catalog.go": {
			`"github.com/drilonrecica/nise-and-go/runtime/authz"`,
			"func Catalog() (*authz.Catalog, error)",
			`RoleAdministrator = "administrator"`,
			`RoleAuditor = "auditor"`,
			`AuditRead = authz.MustPermission("audit.read")`,
		},
		"internal/features/auth/roles.go": {
			"func (r *Roles) Grant(ctx context.Context, userID, role, grantedBy string) error",
			"func (r *Roles) Permissions(ctx context.Context, userID string) (authz.Set, []string, error)",
			"func (r *Roles) Holders(ctx context.Context, role string, limit int) ([]Holder, error)",
			"r.catalog.Role(role); !declared",
		},
		"internal/features/auth/queries/users.sql": {
			"-- name: GrantRole :execrows",
			"ON CONFLICT (user_id, role) DO NOTHING",
			"-- name: RevokeRole :execrows",
			"-- name: ListUserRoles :many",
			"-- name: ListRoleHolders :many",
		},
		"internal/features/auth/roles_test.go": {
			"TestGrantAndRevokeRoles",
			"TestGrantRefusesUndeclaredRolesAndUnknownAccounts",
			"TestPermissionsResolveThroughTheCatalog",
			"TestAStaleGrantContributesNothingAndIsReported",
		},
		"internal/platform/authorization/catalog_test.go": {
			"TestEveryDeclaredPermissionIsBundledBySomeRole",
			"TestAuditorCanSeeButNotChange",
			"TestGrantingNoRolesGrantsNothing",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The role's meaning is code. A grant row carries a name, never a
	// permission list, or two replicas could disagree about what a role means.
	migration := content["db/migrations/00005_authorization.sql"]
	if strings.Contains(migration, "permission") {
		t.Error("the role assignment table stores permissions; role meaning must be code")
	}

	// No wildcard may appear in a declared permission: a pattern is how an
	// accidental grant becomes invisible.
	catalog := content["internal/platform/authorization/catalog.go"]
	if strings.Contains(catalog, `MustPermission("*`) || strings.Contains(catalog, `.*")`) {
		t.Error("the catalog declares a wildcard permission")
	}
}

func TestGeneratedProjectEnforcesAuthorizationInUseCases(t *testing.T) {
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
		"internal/platform/authorization/authorizer.go": {
			"func Require(ctx context.Context, permission authz.Permission) error",
			"func RequireAll(ctx context.Context, permissions ...authz.Permission) error",
			"return &DeniedError{Permission: permission, Reason: ReasonUnresolved}",
			"return &DeniedError{Permission: permission, Reason: ReasonNoSession}",
			"func (r *Resolver) Middleware(next http.Handler) http.Handler",
			"type contextKey struct{}",
		},
		"internal/platform/authorization/authorizer_test.go": {
			"TestRequireDeniesEveryUnresolvedShape",
			"TestRequireAllNeedsEveryPermissionAndRefusesNone",
			"TestResolutionFailureDeniesRatherThanErrors",
			"TestAuthorityCannotBeForged",
		},
		"internal/features/auth/roles.go": {
			"authorization.Require(ctx, authorization.RolesManage)",
			"authorization.Require(ctx, authorization.RolesRead)",
			"func (r *Roles) GrantAsSystem(ctx context.Context, userID, role string) error",
		},
		"internal/features/auth/accounts.go": {
			"authorization.Require(ctx, authorization.UsersManage)",
		},
		"internal/features/audit/audit.go": {
			"authorization.Require(ctx, authorization.AuditRead)",
		},
		"internal/platform/httpapi/problem/problem.go": {
			`"permission_denied"`,
			"func PermissionDenied() Definition",
		},
		"internal/app/app.go": {
			"authorization.NewResolver(roles, permissionCatalog,",
			"authorizationResolver.Middleware",
		},
		"internal/features/auth/roles_test.go": {
			"TestRoleChangesDenyByDefault",
		},
		"internal/features/audit/audit_test.go": {
			"TestReadingTheLogDeniesByDefault",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// Require must never take a permission set: a use case handed the
	// caller's permissions would be as trustworthy as its least careful call
	// site.
	authorizer := content["internal/platform/authorization/authorizer.go"]
	if strings.Contains(authorizer, "func Require(grants Grants") || strings.Contains(authorizer, "func Require(set authz.Set") {
		t.Error("Require accepts a permission set rather than a context")
	}

	// The public refusal must name nothing. Which permission was missing
	// belongs in the log and the audit record.
	problem := content["internal/platform/httpapi/problem/problem.go"]
	if strings.Contains(problem, "does not have permission to do that.\", \"permission_denied\"") == false {
		t.Error("the permission-denied problem text changed shape")
	}

	// The resolver must not reject: that decision belongs to the use case.
	for _, forbidden := range []string{"WriteHeader", "StatusForbidden", "http.Error("} {
		if strings.Contains(authorizer, forbidden) {
			t.Errorf("the authorization resolver rejects requests: it contains %q", forbidden)
		}
	}
}
