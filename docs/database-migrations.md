# Database migrations

Generated applications pin Goose `v3.27.3` and keep migration SQL readable
under `db/migrations/`. `db/embed.gen.go` embeds only `*.sql` files and
returns an `fs.FS` rooted at that directory, so the same bytes reviewed in
source are the bytes carried by the binary.

## Baseline decision

Goose's provider rejects a filesystem with no migration sources. Nise
therefore generates `00001_baseline.sql`, a schema-neutral `SELECT 1`
migration with explicit Up and Down sections. It establishes a stable first
version without guessing at an application's tables, PostgreSQL extensions,
or domain requirements. Application schema begins in later migrations.

This is preferable to creating a framework metadata table: Goose already
owns its version table, while another table would add a permanent schema
contract before it has a concrete use.

## Runtime boundary

`internal/platform/database.OpenMigrator` receives `db.Migrations()`
explicitly. It constructs Goose's instance-scoped provider, disables the
legacy global Go-migration registry, opens one dedicated pgx-backed
`database/sql` connection, proves connectivity, and configures a PostgreSQL
session advisory lock. Opening a migrator never applies a migration.

The server does not migrate on startup. Deployment and operator commands are
the only mutation boundary; application startup may inspect compatibility
but must remain read-only.

## Commands and compatibility

Run `nise db status` to inspect and `nise db migrate` to apply pending
migrations. Both delegate to the generated application's own command surface;
see [`nise db`](commands/db.md) for output and error contracts.

Status and startup use the same read-only compatibility check. Empty or
valid-but-behind schemas are allowed and reported. A database ahead of the
binary, malformed or partial Goose history, or a nonempty schema with no
history fails closed. Status never creates the Goose table.

This check is deliberately about history, not full schema introspection.
Goose does not store migration checksums, so it cannot prove that nobody
manually changed a table or edited already-applied SQL. Applied migration
immutability and controlled production DDL remain operational requirements.

## Authoring rules

- Use zero-padded sequential names: `00002_add_accounts.sql`.
- Include `-- +goose Up` and `-- +goose Down` sections. Production recovery
  normally uses a new forward migration even though Down remains valuable in
  disposable tests and deliberate rollback procedures.
- Never edit or remove a migration that may have been applied. Add a new
  forward fix.
- Do not place secrets, environment-specific values, or dynamic timestamps
  in migration files.
- Keep migrations transactional unless PostgreSQL requires an explicitly
  documented non-transactional operation.

## Testing

The generated package test compares every embedded migration byte-for-byte
with the readable source. Its PostgreSQL integration test uses
`internal/platform/database/dbtest` to create a fresh isolated database from
the administrative `TEST_DATABASE_URL`, then applies and rolls back the
complete embedded history. See [PostgreSQL integration testing](database-testing.md)
for the local-skip, CI-required, connection-limit, and cleanup contracts.

The generated `migration_matrix_test.go` separately proves a clean install and
every schema snapshot under
`internal/platform/database/testdata/migration-upgrades/`. A snapshot is an
immutable SQL dump named `NNNNN_name.sql`, where the prefix is the Goose
version recorded inside it. Version `00001_baseline.sql` represents the first
generated release schema. Never edit a fixture after that release is supported;
add the next release snapshot. Fixtures live outside `db/migrations`, are not
embedded in production binaries, and exist only to prove forward migration to
the current embedded head.

Run the matrix locally with `make migration-test`. Repository CI runs the same
target against PostgreSQL 17.6, so a green build cannot silently skip both the
clean-install and supported-upgrade paths.
