# Database query instrumentation

Generated application servers install one application-owned pgx tracer on
every primary-pool connection. It records bounded query metrics, emits
sanitized slow-query warnings, and supplies deterministic per-context query
budgets for reference-flow tests. It does not record SQL, arguments, copied
rows, results, or database errors.

## What is counted

A query count is a PostgreSQL round trip initiated through pgx:

- one `Query`, `QueryRow`, or `Exec` call is one;
- one `CopyFrom` call is one; and
- one `SendBatch` call is one, regardless of the number of queued statements.

This definition targets N+1 and avoidable network-turn regressions. A batch
can reduce round trips without reducing PostgreSQL work, so code using batches
must separately test its maximum queued statement count and input size. Begin,
commit, rollback, pool acquisition, and statement preparation are not query
budget entries.

The tracer classifies ordinary SQL by its first effective keyword after
leading comments. The complete metric/log label set is `select`, `insert`,
`update`, `delete`, `merge`, `with`, `call`, `ddl`, `transaction`, `copy`,
`batch`, and `other`. Unknown or caller-constructed labels collapse to
`other`; outcomes collapse to `success` or `error`. Raw SQL can therefore
never create a label series or appear in a metric.

## Test budgets

Attach a budget to the context at the boundary of a named reference flow:

```go
ctx, budget, err := database.WithQueryBudget(ctx, 3)
if err != nil {
    t.Fatal(err)
}

// Run the complete use case with ctx.

if got := budget.Count(); got != 3 {
    t.Fatalf("query count = %d, want 3", got)
}
if err := budget.Verify(); err != nil {
    t.Fatal(err)
}
```

Assert the exact count so both additions and unexpected removals receive
review; `Verify` separately expresses the maximum and produces a useful
failure. The counter is concurrency-safe and follows derived contexts, but it
is test instrumentation—not a production circuit breaker. It observes calls
as they start and does not cancel a use case that exceeds its budget. Changing
a named flow's expected count requires a reason in the change that introduced
the new database behavior.

## Metrics

The application registers these metrics on the same bounded registry as HTTP
and pool metrics:

| Metric | Type | Labels |
|---|---|---|
| `db_queries_total` | counter | `statement`, `outcome` |
| `db_query_duration_seconds` | histogram | `statement`, `outcome` |

The closed label sets need at most 24 series. Each vector additionally caps
itself at 32 series and collapses anything beyond the cap into the registry's
fixed overflow series. Duration buckets span 1 ms through 5 seconds.

## Slow-query warnings

`DB_SLOW_QUERY_THRESHOLD` is a required-positive duration with a `250ms`
default. A completed operation at or above the threshold emits one warning
named `slow database query` containing only:

- the bounded statement and outcome labels;
- the observed duration and configured threshold; and
- validated request/correlation IDs when the context carries them.

The warning never contains SQL, pgx arguments, copied rows, returned data,
credentials, or the database error. Errors remain available to the caller and
the ordinary application error path. The tracer uses the application's
centrally redacting logger as an additional defense, not as permission to add
sensitive query attributes.

Short-lived `db status` and `db migrate` command pools do not install this
server telemetry: they have no metrics endpoint or request correlation
context, and migration timing is reported by the command boundary instead.
