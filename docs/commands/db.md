# `nise db`

`nise db` delegates database operations to the generated application's own
command surface. Nise locates the single `cmd/<app>/` entry, builds it into
a private scratch directory with `GOTOOLCHAIN=local`, runs that binary, and
consumes one strict JSON result. Building first means cancellation targets
the application process itself instead of a `go run` parent. The scratch
binary is removed after the command. Nise never imports the application,
reads migration SQL, opens PostgreSQL, or decides what schema the application
needs.

```sh
nise db status
nise db migrate
nise --json db status
```

Run these from the generated project root or any child directory. They use
the application's ordinary `DATABASE_URL`, `DATABASE_PASSWORD`, and
`DB_CONNECT_TIMEOUT` configuration, including `_FILE` secret indirection.
No URL or credential appears in a success result.

The equivalent direct application commands are:

```sh
go run ./cmd/<app> db status
go run ./cmd/<app> db migrate
```

Use the direct form when diagnosing an application-owned build or database
error. Nise deliberately does not echo arbitrary child-process stderr: an
application owner may have changed that code, so treating its output as
safe would create a secret-disclosure boundary. It also caps the generated
application's machine output at 64 KiB before strict decoding.

Nise itself makes no network request. The explicit build subprocess may ask
the configured Go proxy for modules missing from the local module cache; that
is ordinary `go build` behavior and is why `nise db` is not part of the
offline-command guarantee. `GOTOOLCHAIN=local` still prevents an implicit Go
toolchain download.

## Status

`nise db status` is read-only. On a fresh database it does not create
`goose_db_version` or any other object.

| State | Meaning | Startup behavior |
|---|---|---|
| `uninitialized` | The current schema has no user objects and no Goose history. | Allowed, with a warning. |
| `behind` | Goose history is valid but below the binary's embedded head. | Allowed, with a warning. Migrate before enabling code that needs pending schema. |
| `current` | Applied history exactly reaches the embedded head. | Allowed. |

The command fails, and application startup fails closed, for:

- `ahead`: the database contains a version newer than this binary;
- `dirty`: the history table is malformed, duplicated, or contains an
  invalid applied flag or zero-version sentinel;
- `history_missing`: a nonempty schema has no Goose history, or applied
  history has a gap.

Behind is deliberately compatible so deployments can inspect a new binary
before the explicit migration step. That does not promise every feature can
serve against old schema; deployment order remains migrate, then enable code
that requires the migration.

The check validates migration history, not arbitrary schema contents. Goose
does not store migration checksums, so manual DDL or edits to an already
applied SQL file cannot be inferred from `goose_db_version`. Treat applied
migrations as immutable and restrict manual production DDL.

## Migrate

`nise db migrate` first requires a compatible history, then applies all
pending embedded migrations in ascending order through the generated
instance-scoped Goose provider. PostgreSQL session advisory locking prevents
two explicit migration processes from applying concurrently. A second run
is a successful no-op.

There is intentionally no `nise db down` command. Production recovery
normally uses a reviewed forward migration; rollback remains available to
application-owned test and deliberate operator code through the generated
`Migrator.Down` method.

Successful JSON examples:

```json
{"action":"status","state":"behind","current":1,"target":3,"pending":2}
{"action":"migrate","state":"current","current":3,"target":3,"pending":0,"applied":[2,3]}
```

The wrapper rejects unknown fields, trailing JSON, inconsistent counts,
unknown states, mismatched actions, and unordered applied versions before it
emits a result.
