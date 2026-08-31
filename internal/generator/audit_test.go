package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectKeepsAnAppendOnlyAuditLog(t *testing.T) {
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
		"db/migrations/00004_audit.sql": {
			"CREATE TABLE audit_events",
			"CONSTRAINT audit_events_outcome CHECK (outcome IN ('succeeded', 'failed', 'denied'))",
			"CONSTRAINT audit_events_actor_kind CHECK (actor_kind IN ('user', 'system', 'anonymous'))",
			"CONSTRAINT audit_events_detail_size CHECK (pg_column_size(detail) <= 4096)",
			"CREATE INDEX audit_events_occurred_at_idx ON audit_events (occurred_at DESC, id DESC)",
			"-- +goose Down",
		},
		"internal/features/audit/queries/audit.sql": {
			"-- name: AppendAuditEvent :one",
			"-- name: ListAuditEvents :many",
			"-- name: CountAuditEvents :one",
			"-- name: DeleteAuditEventsBefore :execrows",
			"ORDER BY occurred_at DESC, id DESC",
		},
		"internal/features/audit/audit.go": {
			"func (r *Recorder) RecordWithin(ctx context.Context, tx pgx.Tx, event Event) (Record, error)",
			"func (r *Recorder) Sweep(ctx context.Context, retention time.Duration, limit int) (int64, error)",
			"MinRetention = 30 * 24 * time.Hour",
			"logging.IsSensitiveKey(key)",
			"forwarded.ClientIP(ctx)",
			"logging.RequestID(ctx)",
		},
		"internal/features/audit/audit_test.go": {
			"TestRecordWithinCommitsWithItsChange",
			"TestRecordRefusesDetailThatMustNotBeRecorded",
			"TestListFiltersAndPagesNewestFirst",
			"TestSweepCannotBePointedAtRecentEvidence",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The log is append-only. An UPDATE against this table anywhere would
	// make every record in it deniable.
	queries := content["internal/features/audit/queries/audit.sql"]
	if strings.Contains(strings.ToUpper(queries), "UPDATE AUDIT_EVENTS") {
		t.Error("the audit queries contain an UPDATE")
	}
	if strings.Count(strings.ToUpper(queries), "DELETE FROM") != 1 {
		t.Error("the audit queries contain more than the one retention DELETE")
	}
	// The one delete must be bounded by the retention cutoff the use case
	// enforces, not by an arbitrary caller-supplied predicate.
	if !strings.Contains(queries, "expiring.occurred_at < sqlc.arg('cutoff')::timestamptz") {
		t.Error("the retention sweep is not bounded by a cutoff")
	}
	if !strings.Contains(content["internal/features/audit/store/audit.sql.go"], generator.SQLCGeneratedHeader) {
		t.Error("the audit store does not carry sqlc's ownership marker")
	}

	// A foreign key from actor_id to users would make an audit row vanish
	// with its subject, which is the opposite of what evidence is for.
	migration := content["db/migrations/00004_audit.sql"]
	if strings.Contains(migration, "REFERENCES users") {
		t.Error("audit_events references users; deleting a user would delete the record of what they did")
	}
}
