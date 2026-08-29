# 0015: Keep transaction ownership in use cases

- **Status:** Accepted
- **Date:** 2026-08-29
- **Amends:** [ADR 0011](0011-runtime-public-api.md)

## Context

sqlc-generated query sets accept a narrow `DBTX`, and pgx pools and
transactions both satisfy it. That structural convenience does not decide who
owns begin, commit, and rollback. If repositories begin transactions, one
business operation can accidentally commit several independent units. If a
transaction is hidden in `context.Context`, call sites no longer show which
queries share atomicity. Repeating lifecycle code in every use case also makes
panic, cancellation, and rollback-error handling drift.

The runtime API list in ADR 0011 intentionally required a new ADR before it
could grow. Transaction lifecycle is the first later concern with enough
concrete behavior to justify that growth.

## Decision

Add the stdlib-only `runtime/transaction` package. Its generic `Runner[T]`
owns lifecycle only: an explicit `BeginFunc[T]` supplies the driver operation,
and `T` must expose `Commit` and `Rollback`. The generated application adapts
`pgxpool.BeginTx`; runtime never imports pgx.

A use case calls `Within`, selects closed isolation/access enums, and receives
both a derived context and the concrete `pgx.Tx`. It passes that transaction to
its feature-local sqlc `New` constructor. Repositories execute against the
received `DBTX` and never begin or commit. The transaction itself is not stored
in context; context carries only an unexported active marker used to reject a
nested `Within` call when callers correctly propagate the callback context.
Nested behavior is an error, not an implicit savepoint and not an independent
inner transaction.

The lifecycle contract is:

- successful work commits once;
- work errors roll back, preserving both work and rollback errors;
- cancellation after work rolls back and returns the context error;
- commit failure is returned and triggers best-effort rollback;
- panic triggers best-effort rollback and re-panics the identical value;
- rollback uses a context detached from cancellation but bounded to five
  seconds by default, configurable at runner construction.

A rollback error during panic cannot be returned without replacing or wrapping
the panic value, so it is intentionally discarded after the bounded attempt.
Applications that need to observe such cleanup failure must do so in their pgx
adapter or database instrumentation; the runner preserves panic identity.

The generated `internal/platform/database.Transactor` maps options to pgx and
is application-owned. `internal/app` constructs it beside the primary pool.
No handler or repository receives it by default; later feature constructors
receive it explicitly.

## Alternatives considered

- **Generated lifecycle code only.** Rejected because the rollback and panic
  contract is driver-independent and security-sensitive enough that copies
  would drift.
- **A pgx-dependent runtime package.** Rejected because driver mapping belongs
  to the generated application and would make the shared runtime unnecessarily
  depend on pgx.
- **Transaction in context.** Rejected because it hides a critical dependency,
  encourages repositories to reach for ambient state, and cannot express the
  sqlc constructor boundary clearly.
- **Automatic nested savepoints.** Rejected because savepoint rollback is not
  equivalent to a nested business transaction and implicit partial recovery is
  difficult to reason about.

## Consequences

The public runtime surface grows by one package and the exported transaction
types follow the runtime stability policy. Use-case code imports
`runtime/transaction` for options and its application-local database adapter
for execution. The callback context must be propagated; deliberately replacing
it with an ancestor context can bypass the nested-use marker, so code review
and tests remain part of the ownership convention.
