# Direct pgx escape hatch

sqlc is the default database boundary in a generated application. Direct pgx
is an exceptional, reviewed adapter for a PostgreSQL operation that sqlc cannot
express well; it is not a second general-purpose repository style.

## Allowed cases

A direct-pgx adapter is appropriate only for one of these concrete needs:

- PostgreSQL protocol operations such as `CopyFrom` that sqlc does not model.
- A PostgreSQL-specific capability whose lifecycle cannot be represented as a
  normal query. A long-lived `LISTEN` connection, for example, belongs in a
  dedicated platform adapter rather than a feature repository.
- A measured hot path where a benchmark and query plan justify departing from
  generated queries.

Convenience, speculative performance, dynamic query construction, migrations,
or avoiding a sqlc configuration are not reasons to use the escape hatch. It
must not bypass authorization, row-level-security setup, transaction ownership,
or query observation.

## Adapter shape

Keep the adapter beside the feature that owns the operation. Give it a
consumer-owned interface containing only the pgx methods it calls, static SQL,
fixed table and column identifiers, and ordinary bound values. Do not create a
shared `RawRepository`, expose `*pgx.Conn` or `PgConn`, interpolate values with
`fmt`, or let callers supply arbitrary SQL.

The generated
`internal/platform/database/directpgx_test.go` is an application-owned,
compile-checked example. Its interface is satisfied by `pgx.Tx`, supports only
`Exec` and `CopyFrom`, and deliberately cannot acquire a connection or drop to
the wire protocol. Copy that shape into the owning feature and rename it for
the operation; do not turn the example into a generic base abstraction.

## Transactions and observation

The use case still owns the business transaction. It calls
`database.Transactor.Within`, constructs the direct adapter from the callback's
`pgx.Tx`, and propagates the callback context. An adapter never starts,
commits, or rolls back a business transaction. A truly non-transactional read
may receive a similarly narrow pool interface through constructor wiring; it
must not discover a global pool.

Stay on pgx public operations covered by its tracing contracts:
`Query`/`QueryRow`/`Exec`, `Batch`, and `CopyFrom`. Their SQL may be observed,
but arguments, copied rows, credentials, and result bodies must not be logged.
Dropping to `PgConn` or raw protocol methods requires a dedicated platform
adapter, custom bounded instrumentation, and a separate design review because
the normal query tracer no longer proves coverage. See [Observability](observability.md)
for the surrounding logging and metrics policy.

Errors should add operation context and preserve the wrapped cause, without
including SQL, identifiers derived from input, or argument values.

## Review and test contract

Before accepting a direct-pgx adapter, verify all of the following:

- The exceptional PostgreSQL need and rejected sqlc form are documented beside
  the adapter. A performance exception includes its reproducible evidence.
- The interface contains only the required traced methods; SQL and identifiers
  are static or selected from a closed allowlist, and values remain bound.
- The use case owns the transaction and passes both callback context and
  callback `pgx.Tx` into the adapter.
- Unit tests assert exact SQL, bound argument shape, identifiers, row counts,
  empty-input behavior, and wrapped errors.
- A disposable-PostgreSQL integration test proves commit and rollback behavior.
- Query-budget and slow-query tests prove the operation remains visible without
  recording arguments or copied row data.
