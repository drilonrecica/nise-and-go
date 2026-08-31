package generator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestGeneratedProjectDefinesCommandIdempotency(t *testing.T) {
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
		"db/migrations/00002_idempotency_keys.sql": {
			"-- +goose Up",
			"CREATE TABLE idempotency_keys",
			"CONSTRAINT idempotency_keys_pkey PRIMARY KEY (scope, idempotency_key)",
			"CONSTRAINT idempotency_keys_fingerprint_length CHECK (octet_length(request_fingerprint) = 32)",
			"CONSTRAINT idempotency_keys_response_status CHECK (response_status BETWEEN 100 AND 599)",
			"CONSTRAINT idempotency_keys_lifetime CHECK (expires_at > created_at)",
			"CREATE INDEX idempotency_keys_expires_at_idx",
			"-- +goose Down",
			"DROP TABLE idempotency_keys",
		},
		"internal/platform/idempotency/idempotency.go": {
			"func NewExecutor(transactor *database.Transactor, retention time.Duration, options ...Option) (*Executor, error)",
			"func (e *Executor) Do(ctx context.Context, scope, key string, fingerprint []byte, command Command) (Result, error)",
			"e.transactor.Within(ctx, transaction.Options{}",
			"SELECT pg_try_advisory_xact_lock($1, $2)",
			"ErrConcurrentRequest",
			"ErrKeyReuse",
			"func Fingerprint(method, target string, body []byte) []byte",
			"func ValidateKey(key string) error",
			"func DeleteExpired(ctx context.Context, db Queryer, now time.Time, limit int) (int64, error)",
		},
		"internal/platform/idempotency/idempotency_test.go": {
			"TestExecutorRunsACommandOnceAndReplaysIt",
			"TestExecutorRefusesAKeyReusedForADifferentRequest",
			"TestExecutorRollbackLeavesNoRecord",
			"TestExecutorRunsOnceUnderConcurrency",
			"TestExecutorExpiresARecordedResponse",
			"TestDeleteExpiredIsBounded",
			"dbtest.New(t)",
		},
		"internal/platform/httpapi/api.go": {
			"func idempotencyKey(r *http.Request) (string, error)",
			"func idempotencyFingerprint(r *http.Request, requestBody any) ([]byte, error)",
			"func idempotencyProblem(err error) problem.Definition",
			"errors.Is(err, idempotency.ErrConcurrentRequest)",
			"errors.Is(err, idempotency.ErrKeyReuse)",
		},
		"internal/platform/httpapi/problem/problem.go": {
			`"invalid_idempotency_key"`,
			`"idempotency_conflict"`,
			`"idempotency_key_reuse"`,
			"http.StatusConflict",
			"http.StatusUnprocessableEntity",
		},
		"internal/platform/httpapi/api_test.go": {
			"TestIdempotencyKeyIsRequiredAndBounded",
			"TestIdempotencyFingerprintIgnoresOnlyWhatItShould",
			"TestIdempotencyProblemDistinguishesRetryableFailures",
		},
		"api/openapi.yaml": {
			"    IdempotencyKey:",
			"name: Idempotency-Key",
			"in: header",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The migration and the package it serves are both application-owned:
	// a schema Nise rewrote on upgrade would destroy an application's own
	// later migrations.
	for _, path := range []string{
		"db/migrations/00002_idempotency_keys.sql",
		"internal/platform/idempotency/idempotency.go",
		"internal/platform/idempotency/idempotency_test.go",
	} {
		if owners[path] != generator.OwnerApp {
			t.Errorf("%s owner = %q, want %q", path, owners[path], generator.OwnerApp)
		}
	}

	// The recorded-body ceiling in SQL and in Go must be the same number.
	// Two different limits would mean a response the executor accepted and
	// the database refused.
	if !strings.Contains(content["db/migrations/00002_idempotency_keys.sql"], "octet_length(response_body) <= 1048576") {
		t.Error("the migration's response-body ceiling does not match MaxResponseBytes")
	}
	if !strings.Contains(content["internal/platform/idempotency/idempotency.go"], "MaxResponseBytes = 1 << 20") {
		t.Error("MaxResponseBytes is not the 1 MiB the migration and transport limits assume")
	}

	document, err := openapi3.NewLoader().LoadFromData([]byte(content["api/openapi.yaml"]))
	if err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate generated OpenAPI: %v", err)
	}
	parameter := document.Components.Parameters["IdempotencyKey"]
	if parameter == nil || parameter.Value == nil {
		t.Fatal("OpenAPI lacks the IdempotencyKey parameter component")
	}
	if parameter.Value.In != openapi3.ParameterInHeader || parameter.Value.Name != "Idempotency-Key" {
		t.Errorf("IdempotencyKey = %q in %q, want Idempotency-Key in header", parameter.Value.Name, parameter.Value.In)
	}
	if !parameter.Value.Required {
		t.Error("IdempotencyKey is optional; a command that is only sometimes idempotent is not idempotent")
	}
	schema := parameter.Value.Schema
	if schema == nil || schema.Value == nil {
		t.Fatal("IdempotencyKey has no schema")
	}
	if schema.Value.MinLength != 8 || schema.Value.MaxLength == nil || *schema.Value.MaxLength != 255 {
		t.Errorf("IdempotencyKey bounds = %v..%v, want 8..255", schema.Value.MinLength, schema.Value.MaxLength)
	}
}
