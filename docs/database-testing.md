# PostgreSQL integration testing

Generated applications include `internal/platform/database/dbtest`, a reusable
test-only harness that creates one real PostgreSQL database per test. Tests do
not share schemas, Goose history, tables, or pooled session state.

Set `TEST_DATABASE_URL` to an administrative database on a disposable
PostgreSQL server. It must use `postgres://` or `postgresql://` URL syntax and
the role must have `CREATEDB`. A password may be in that URL or supplied as
`TEST_DATABASE_PASSWORD`; returned test URLs always remove the password and
expose it separately as `runtime/config.Secret`.

```sh
TEST_DATABASE_URL='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable' \
  go test ./internal/platform/database/...
```

Use `make migration-test` to run only the race-enabled clean-install and
supported-release upgrade matrix. The target requires the same environment and
is the command the generated application's CI should gate.

Use the harness from an integration test:

```go
testDatabase := dbtest.New(t)
pool, err := database.Open(
	ctx,
	testDatabase.URL().Reveal(),
	testDatabase.Password().Reveal(),
	testSettings,
)
```

`dbtest.New` creates a cryptographically unique, identifier-safe database from
`template0`, with a PostgreSQL-enforced connection limit of eight. It registers
cleanup immediately after creation. Cleanup uses a separate admin context and
`DROP DATABASE ... WITH (FORCE)`, so leaked test connections cannot silently
leave a database behind. Cleanup failure fails the test and names only the
random database, never a URL or password.

The administrative context is bounded at sixty seconds, deliberately generously.
Creating and dropping a database copies or removes a whole directory, the two
serialize against each other on the server, and `go test ./...` runs several
packages at once — so on a busy machine a drop that normally takes milliseconds
can take tens of seconds. A tight bound there fails a test that has already
passed. The timeout exists to stop a hung administrative connection from
blocking forever, not to measure anything.

Testing cleanup is LIFO: pools and migrators registered after `dbtest.New`
close first, then the database is dropped. The force option is a final safety
net, not a substitute for closing resources. PostgreSQL 17.6 is the generated
development baseline; forced drop has been available since PostgreSQL 13.

## Local and CI policy

When `TEST_DATABASE_URL` is absent locally, integration tests explicitly skip.
When `CI=true` or `CI=1`, absence is a test failure. This prevents a green CI
run that silently exercised no PostgreSQL behavior while keeping ordinary
unit-test runs usable for developers without a local server.

The administrative server must itself be disposable test infrastructure. The
harness never drops the database named by `TEST_DATABASE_URL`, but it does
create and forcibly drop random databases on that server. Do not give the test
role access to a production cluster.
