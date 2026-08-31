# Background jobs

Every generated application has a background job system, and it needs no
process the application does not already run. The queue is the PostgreSQL
database that is already there.

That single fact is what the rest of this page follows from. It is why a job
can be enqueued inside the transaction that made the change requiring it, why
there is no message broker in the [dependency
allowlist](dependencies.md), and why the job schema is a numbered migration in
the project's own history rather than something a second tool maintains.

The runner is [River](https://riverqueue.com), reached through pgx. River owns
fetching, retry timing, and leader election. The generated
`internal/platform/jobs` package owns the boundary the application sees.

## What a generated project starts with

- `db/migrations/00009_jobs.sql` — River's schema.
- `internal/platform/jobs/` — the `Registry` and `Client` types.
- `internal/app/jobs.go` — `registerJobs`, where job kinds are declared. It
  starts empty.
- `JOB_MAX_WORKERS` and `JOB_POLL_INTERVAL` in `.env.example`.

A freshly generated application registers no job kinds. A worker process
starts, logs `works_jobs=false`, and does nothing until the first job is
written. That is deliberate: an empty worker that says it is empty is easier
to reason about than a placeholder job that appears to do something.

## Enqueuing

```go
// Inside a use case that already owns a transaction.
err := transactor.InTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
    if err := invoices.MarkPaid(ctx, tx, id); err != nil {
        return err
    }
    return jobClient.InsertTx(ctx, tx, SendReceiptArgs{InvoiceID: id}, nil)
})
```

The job row is written by the same transaction as the business change, so it
becomes visible if and only if that change committed. A rollback takes the job
with it; there is no window in which one exists without the other.

`Client.Insert` enqueues outside a transaction — and **refuses to be called
from inside one**:

```
jobs: Insert was called inside a transaction; use InsertTx with that
transaction so the job commits with the change that needs it (kind
"send_receipt")
```

The refusal is the point. An `Insert` made while a transaction is open is two
operations that can disagree, in both directions: the job is enqueued and the
change then fails, or the change commits and the job was never written.
Neither shows up in review, both are intermittent in production, and both have
the same one-word fix. So rather than document the hazard and hope, the client
refuses, names the alternative, and refuses before writing anything.

It knows from the context. `runtime/transaction` marks the context it hands
its callback, and a use case is required to propagate that context anyway;
`transaction.IsActive` is the read-only predicate built on it. The transaction
itself is never fished out of the context — it is a parameter, so a reader can
see at the call site which transaction a job joined.

A caller that has genuinely decided a job must be enqueued whether or not the
surrounding change commits calls `Insert` after `Within` returns, which is
also where a reader would look for that decision.

## Depend on the narrow interface

A use case needs to put work on the queue. It does not need to start the
client, stop it, or ask what kinds exist, and taking `*jobs.Client` as a
parameter would let it do all three.

```go
type Enqueuer interface {
    Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) error
    InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) error
}
```

`*jobs.Client` satisfies it, and a test satisfies it with a recorder in four
lines.

## Declaring a job

A job is two types: an argument type that declares its `Kind`, and a worker.

```go
type SendReceiptArgs struct {
    InvoiceID string `json:"invoice_id"`
}

// The kind is a name, not a Go type path. Renaming the Go type must not
// change it, or jobs already sitting in the queue stop matching a worker.
func (SendReceiptArgs) Kind() string { return "send_receipt" }

type SendReceiptWorker struct {
    river.WorkerDefaults[SendReceiptArgs]
    mailer Mailer
}

func (w *SendReceiptWorker) Work(ctx context.Context, job *river.Job[SendReceiptArgs]) error {
    return w.mailer.SendReceipt(ctx, job.Args.InvoiceID)
}
```

It becomes runnable by being registered, in `internal/app/jobs.go`:

```go
func registerJobs(registry *jobs.Registry) {
    jobs.Register(registry, &SendReceiptWorker{mailer: mailer})
}
```

There is no `init()` registration, no reflection over a package, and no
discovery step. A job runs because a line of ordinary code says so, and the
kinds a process knows about are printed in its startup log so that "the job
never ran" and "this deployment does not know about that job" are
distinguishable without a database query.

## What is actually proved

`internal/platform/jobs/jobs_postgres_test.go` runs against a real, isolated
PostgreSQL database, because nothing here can be proved without one — the
claim is about what a second connection sees after `COMMIT` and after
`ROLLBACK`, which is a property of PostgreSQL rather than of any code in this
project. It pins four things:

- A job enqueued inside an open transaction is **not** visible to another
  connection until that transaction commits.
- After the commit, it is.
- After a rollback, it never existed.
- `Insert` inside a transaction is refused, and writes nothing.

The counts are read straight out of `river_job` on a separate connection.
Reading them back through River's own API would prove only that River agrees
with itself.

## Every job must be safe to run twice

A process killed mid-job leaves its row in the `running` state, and the next
client to hold the leader lease rescues it. There is no exactly-once delivery
to opt into — not here, and not in any queue worth trusting. Write the work so
that running it a second time is either harmless or detectably redundant.

## Which processes work jobs

Every process builds a job client, because every process may need to enqueue.
Only a process running in mode `all` or `worker` starts one.

That separation is not a convenience. A client configured with a queue fetches
work, so a web replica built the same way as a worker would quietly become a
worker, and scaling the web tier would scale job concurrency along with it.
See [operations](operations-runtime.md) for the process modes.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `JOB_MAX_WORKERS` | `10` | How many jobs one worker process runs at once. |
| `JOB_POLL_INTERVAL` | `5s` | Backstop interval for finding work a notification was missed for. |

Both are per worker process, not per deployment.

`JOB_MAX_WORKERS` is chosen against `DB_MAX_CONNS`, not against the CPU count.
Every running job holds work the connection pool has to serve, so raising it
past the pool size does not add throughput — it converts throughput into
connection waiting, which is harder to see and harder to explain. Both values
are refused rather than clamped when they are impossible: an operator should
learn at startup that a setting was wrong, not discover later that it was
silently replaced.

River learns about almost every job immediately, through a PostgreSQL
notification. `JOB_POLL_INTERVAL` is what bounds the delay when one is missed,
which is the difference between a dropped notification delaying a job and
losing it.

## Shutdown

On shutdown the worker stops fetching and gives running jobs a fixed window to
finish. A job still running when that window closes is left in the `running`
state and rescued later — which is the same reason a job must be safe to run
twice, arriving from the other direction.

## Why the schema is in this project's migration history

River ships its own migrator. Generated applications do not use it.

A generated project has exactly one migration history, applied by one explicit
command, verified by one compatibility check that requires the version
sequence to have no gaps. A second migrator writing to the same database on
its own schedule breaks all three, and puts part of the schema outside what an
operator reviews before a deploy.

`00009_jobs.sql` is therefore River's own migration line 001 through 007 for
the pgx v5 driver, squashed into the end state they produce. River supports
exactly this: its migration 005 carries an explicit branch for users running
their own migration system. The file also writes the applied line versions
into `river_migration`, so River's tooling can be pointed at the database later
and correctly conclude that work is already done.

When River is upgraded, read its changelog for new migration versions, add the
corresponding statements as the next forward migration, and record the new
version numbers. Never edit a migration that has already been applied. See
[database migrations](database-migrations.md).
