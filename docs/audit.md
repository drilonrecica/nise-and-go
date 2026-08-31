# Audit Log

Operational logs and audit records answer different questions and have
different lifetimes.

A log line is for the person debugging this deployment this week. It is
unstructured enough to say anything, sampled, rotated, and shipped somewhere
that may or may not still have it next month. An audit record is for the person
asking, months later, who disabled that account — so it is a row, in the same
database as the thing it describes, with a shape a query can rely on.

They are separate on purpose. Neither is a substitute for the other, and a
system that keeps only logs has no answer to the second question.

## Append-only

Nothing in the application updates an audit record. There is no `UPDATE` query
against the table anywhere, and the only `DELETE` is a retention sweep that
**cannot be pointed at anything recent**: `Sweep` refuses a retention shorter
than 30 days. That bound is the point of the method existing rather than a plain
delete — a sweep that could be aimed at yesterday would make the log erasable by
whoever can set a configuration value, which is precisely the person an audit
log exists to record.

There is deliberately no foreign key from `audit_events.actor_id` to `users`.
Deleting a user must not delete, or fail on, the record of what they did; a row
that cascades away with its subject is worthless as evidence.

## What a record holds

| Column | Meaning |
|---|---|
| `occurred_at`, `recorded_at` | When it happened as the application saw it, and when the row was written. They differ for an event recorded from a job or a retried request, and an investigator needs both. |
| `action` | A dotted lowercase name, `session.created`. |
| `outcome` | `succeeded`, `failed`, or `denied`. |
| `actor_kind`, `actor_id` | `user` with an account, or `system`/`anonymous` with none. |
| `subject_kind`, `subject_id` | What was acted on. Both or neither. |
| `client_ip`, `request_id`, `correlation_id` | Where it came from and which request it belonged to. |
| `detail` | Bounded, non-sensitive context as a small JSON object. |

`denied` is separate from `failed` because a run of denials is a different
signal from a run of errors: one is somebody trying, the other is something
broken.

**Request identity is not a parameter.** The recorder reads the client address,
request ID, and correlation ID from the context, so a call site cannot forget
them and cannot fill them in with something other than what the request actually
carried.

**A detail key the logger would redact is refused, not redacted.** The rule is
`logging.IsSensitiveKey`, the same one the log handlers apply, rather than a
second list that would drift. Refusing rather than redacting means a caller who
tried to put a credential in an audit record finds out at the call site.

Every bound — 16 fields, 40-byte keys, 256-byte values, 4 KiB encoded — exists
in the database as a constraint as well as in Go, so a bug in application code
cannot write a row the query API could not read back.

## Recording

```go
// For something already done and not undoable.
_, err := recorder.Record(ctx, audit.Event{
        Action: "session.created", Outcome: audit.Succeeded,
        ActorKind: audit.ActorUser, ActorID: userID,
        SubjectKind: "session", SubjectID: sessionID,
})

// For a change still in flight: the record and the change commit together.
err := transactor.Within(ctx, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
        // … the change …
        _, err := recorder.RecordWithin(ctx, tx, event)
        return err
})
```

`RecordWithin` is the form that matters for anything worth auditing. There is no
window in which the change exists without its record, or the record without its
change, in either direction.

## Querying

`List` pages newest first, after an optional `(occurred_at, id)` position. Both
fields are needed: several events commonly share an instant — one request often
records more than one — and paging on the timestamp alone would skip or repeat
them at the boundary. `Count` answers the same filter's total for a reporting
page.

Filters narrow by action, actor, subject, and time range; every index leads with
the `(occurred_at DESC, id DESC)` pair the listing orders by.

## Related

- [Sessions](sessions.md)
- [Observability](observability.md)
- [Security model](security.md)
