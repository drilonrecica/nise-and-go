package generator_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// planContent renders one option set and returns its files by path.
func planContent(t *testing.T, options generator.Options) map[string]string {
	t.Helper()

	files, err := generator.Plan(options)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
	}
	return content
}

func totpOptions() generator.Options {
	opts := defaultOptions()
	opts.Modules = []recipe.Module{recipe.ModuleTOTP}
	return opts
}

// A module that is not selected contributes no file at all — not an empty one,
// not a stub, not a build-tagged placeholder. That is the whole of the module
// mechanism: there is no registry and nothing to discover.
func TestAnUnselectedModuleContributesNothing(t *testing.T) {
	t.Parallel()

	without := planContent(t, defaultOptions())
	with := planContent(t, totpOptions())

	for path := range without {
		if strings.HasPrefix(path, "internal/features/mfa/") {
			t.Errorf("a project generated without the TOTP module contains %s", path)
		}
	}
	for _, path := range []string{
		"internal/features/mfa/mfa.go",
		"internal/features/mfa/mfa_test.go",
		"internal/features/mfa/queries/mfa.sql",
		"internal/features/mfa/sqlc.yaml",
		"internal/features/mfa/store/mfa.sql.go",
		// Numbered 00010 because this project selected only TOTP: module
		// migrations are numbered contiguously after the core history, in
		// module order, so the number depends on which other modules were
		// selected. The all-modules case is pinned in jobs_test.go.
		"db/migrations/00010_totp.sql",
	} {
		if _, ok := with[path]; !ok {
			t.Errorf("a project generated with the TOTP module lacks %s", path)
		}
	}

	// Nothing in a project without the module mentions it.
	for path, body := range without {
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		if strings.Contains(body, "/internal/features/mfa") {
			t.Errorf("%s imports the mfa feature in a project generated without it", path)
		}
	}
}

// The module's migration is numbered contiguously after the core history. A gap
// is how a missing migration hides, and the runtime's compatibility check
// refuses one.
func TestModuleMigrationsAreContiguous(t *testing.T) {
	t.Parallel()

	with := planContent(t, totpOptions())
	versions := make([]string, 0, 16)
	for path := range with {
		if strings.HasPrefix(path, "db/migrations/") && strings.HasSuffix(path, ".sql") {
			versions = append(versions, strings.SplitN(strings.TrimPrefix(path, "db/migrations/"), "_", 2)[0])
		}
	}
	if len(versions) < 2 {
		t.Fatalf("found %d migrations", len(versions))
	}
	seen := make(map[string]bool, len(versions))
	for _, version := range versions {
		seen[version] = true
	}
	for i := 1; i <= len(versions); i++ {
		want := fmt.Sprintf("%05d", i)
		if !seen[want] {
			t.Errorf("the migration history has no version %s, so it is not contiguous", want)
		}
	}
}

func TestGeneratedProjectWiresTheSecondFactor(t *testing.T) {
	t.Parallel()

	with := planContent(t, totpOptions())
	without := planContent(t, defaultOptions())

	// The login use case names the second factor whether or not the module is
	// selected: an application with none declares that it has none.
	for _, content := range []map[string]string{with, without} {
		for _, fragment := range []string{
			"type SecondFactor interface {",
			"type NoSecondFactor struct{}",
			"var ErrSecondFactorRequired = errors.New(",
			"func (c *Credentials) CompleteSecondFactor(ctx context.Context, challengeToken, code string) (Login, error)",
		} {
			if !strings.Contains(content["internal/features/auth/secondfactor.go"]+content["internal/features/auth/login.go"], fragment) {
				t.Errorf("the auth feature lacks %q", fragment)
			}
		}
	}

	// The wiring is a readable line in either case, and the signature does not
	// change, so the call sites do not branch.
	if !strings.Contains(without["internal/app/secondfactor.go"], "return auth.NoSecondFactor{}, nil") {
		t.Error("a project without the module does not declare that it has no second factor")
	}
	if !strings.Contains(with["internal/app/secondfactor.go"], "mfa.New(transactor, auditor, issuer)") {
		t.Error("a project with the module does not construct the second-factor use case")
	}
	for _, content := range []map[string]string{with, without} {
		if !strings.Contains(content["internal/app/app.go"], "SecondFactor(transactor, auditRecorder,") {
			t.Error("app.go does not take its second factor from the module wiring")
		}
		if !strings.Contains(content["internal/app/enroll.go"], "SecondFactor(transactor, auditRecorder,") {
			t.Error("the enrollment command wires a different second factor than the server")
		}
	}

	// modules.gen.go is nise-owned and must stay free of the application's
	// module path, which is what lets `nise check` re-plan a renamed project.
	for _, content := range []map[string]string{with, without} {
		if strings.Contains(content["internal/app/modules.gen.go"], "\nimport (") {
			t.Error("modules.gen.go has imports, so it now depends on the module path")
		}
	}
}

func TestGeneratedSecondFactorRefusesTheDangerousShortcuts(t *testing.T) {
	t.Parallel()

	with := planContent(t, totpOptions())

	wants := map[string][]string{
		"db/migrations/00010_totp.sql": {
			"CREATE TABLE totp_secrets",
			"CREATE TABLE recovery_codes",
			"CREATE TABLE mfa_challenges",
			"last_step    bigint",
			"CONSTRAINT recovery_codes_digest_length CHECK (octet_length(code_digest) = 32)",
			"-- +goose Down",
		},
		"internal/features/mfa/queries/mfa.sql": {
			"AND (last_step IS NULL OR last_step < $2)",
			"AND used_at IS NULL",
			"AND confirmed_at IS NULL",
		},
		"internal/features/mfa/mfa.go": {
			"func (f *Factors) Complete(ctx context.Context, rawToken, code string) (auth.Completion, error)",
			"func (f *Factors) countAttempt(ctx context.Context, challengeID pgtype.UUID) error",
			"reauth.Require(ctx, reauth.ActionSecondFactorDisable)",
			"MaxChallengeAttempts = 5",
		},
		"internal/platform/reauth/matrix.go": {
			`ActionSecondFactorDisable Action = "second_factor.disable"`,
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(with[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// A wrong code has to cost an attempt even though the transaction that
	// checked it rolls back. A budget that only recorded the guesses that
	// happened to commit would not be a budget.
	feature := with["internal/features/mfa/mfa.go"]
	complete := feature[strings.Index(feature, "func (f *Factors) Complete("):]
	complete = complete[:strings.Index(complete, "\n// countAttempt")]
	if strings.Contains(complete, "CountChallengeAttempt") {
		t.Error("the attempt is counted inside the transaction that is about to roll back")
	}
	if !strings.Contains(complete, "f.countAttempt(ctx, attempted)") {
		t.Error("a refused code does not cost the challenge an attempt")
	}

	// The recovery-code digest is what is stored, never the code.
	queries := with["internal/features/mfa/queries/mfa.sql"]
	if strings.Contains(queries, "RETURNING code_digest") {
		t.Error("a query hands back a recovery-code digest")
	}
}
