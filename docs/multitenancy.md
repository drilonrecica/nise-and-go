# Multitenancy and row-level security

The `organizations` compile-time module. A project generated without it has no
organizations, no tenant context, and no row-level security.

## Two layers, and why both

The application checks membership before it acts, so a person who is not in an
organization gets a clear refusal rather than an empty list. PostgreSQL
refuses regardless, through row-level security.

Neither is redundant. A permission check without RLS means every new query is
a chance to leak. RLS without a permission check means a missing tenant
context looks like an empty organization instead of a mistake. The first
failure is a breach and the second is a bug report, which is the correct
asymmetry.

## The mechanism

Every tenant-scoped table carries `org_id` and has a policy comparing it
against `app.current_org_id`, which the application writes with **`SET
LOCAL`** at the start of each transaction.

`SET LOCAL` is the whole design. It is reverted when the transaction ends — by
commit, by rollback, by error, by anything — so a pooled connection handed to
the next request cannot still be carrying the previous request's tenant. A
plain `SET` persists on the connection for as long as the pool keeps it, and
the first time somebody forgets to clear it, one tenant reads another's data
through a connection that was simply reused.

```go
err := transactor.WithinTenant(ctx, orgID, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
    // Every statement here is inside orgID. Nothing else is visible.
})
```

There is deliberately no way to clear the tenant and keep the transaction, and
no way to change it partway through. A transaction belongs to one tenant;
needing two means needing two transactions.

## The three things that make this real

Row-level security is unusually easy to configure into something that looks
complete and enforces nothing. Three specific facts decide it, and all three
are checked rather than assumed.

### 1. FORCE, not just ENABLE

`ENABLE ROW LEVEL SECURITY` **does not apply to a table's owner.** Migrations
run as the owner, and most deployments connect as the same role — so a schema
with only `ENABLE` has policies that are never consulted. It passes every test
that checks `pg_policies`.

Both tables are `FORCE ROW LEVEL SECURITY`, and a test reads
`pg_class.relforcerowsecurity` to say so in one line rather than leaving it to
be inferred from a behavioural failure.

### 2. The connecting role must not be a superuser

**A superuser bypasses every policy, and so does any role with `BYPASSRLS`.**
`FORCE` does not change that: it removes the owner's exemption, not the
superuser's.

This is the failure that produces false confidence rather than an error. The
schema is right, the policies exist, and a deployment connecting as `postgres`
has no tenant isolation at all.

So the application asks at startup, through `database.CheckTenantIsolation`,
and **production refuses to start** when the answer is wrong. Development
warns loudly instead — a developer running against a local superuser has not
made a mistake worth blocking, but has made one worth knowing about.

The generated tests run as a role created for the purpose, which is neither a
superuser nor `BYPASSRLS`, and they assert that premise before asserting
anything else. Run as the administrative role, every isolation test passes
vacuously — which is exactly what they did before this was fixed.

### 3. WITH CHECK, not only USING

`USING` decides what a statement may read or modify. `WITH CHECK` decides what
it may leave behind.

A policy with only `USING` lets a tenant **write** a row belonging to another
tenant — it just cannot read it back afterwards, which is a poor consolation
and an excellent way to corrupt somebody else's data. Both policies carry
both.

## The failure mode is empty, not everything

`current_tenant()` returns `NULL` when nothing established a tenant, every
policy compares against it, and a comparison with `NULL` is not true. So a
query that forgot its tenant context returns **no rows**.

That direction was chosen. A forgotten `SET LOCAL` produces an empty page,
which somebody notices and reports. The other direction produces another
tenant's data, which nobody notices at all.

The function takes `current_setting('app.current_org_id', true)` — the `true`
is "missing_ok", and without it an unset variable *raises* instead of
returning `NULL`. That sounds stricter and is worse: an error is caught and
logged somewhere, while an empty result is seen by a person.

## Creating an organization

A policy of the form `id = current_tenant()` refuses the insert that creates
the tenant, correctly and inconveniently.

The resolution is not an exemption. `Transactor.WithinNewTenant` mints the
identifier inside the transaction, establishes it as the tenant, and only then
runs the work — so the transaction genuinely *is* in that tenant, the ordinary
policy applies to every statement, and the organization it creates is the only
row it can create. A permissive INSERT policy would have opened the one
statement that decides who owns what; a privileged escape hatch would have
been a second write path with no policy on it.

Ownership is established in the same transaction, so a failure after the first
statement cannot leave an organization nobody can administer.

## Tenant identifiers are canonical

`WithinTenant` refuses anything that is not a UUID in canonical form —
lowercase, hyphenated, 36 characters.

The value is passed as a bound parameter, so this is not what stands between
the application and injection. It does two other things. A malformed value
would make every policy comparison fail with a cast error rather than
returning no rows, turning a default-deny into a 500. And `pgtype` accepts a
UUID with the hyphens omitted and with uppercase hex, so three different
strings would otherwise name one tenant — the same "two spellings, one object"
hazard that makes an authorization decision unreliable, because it is made
about one spelling and applied to another.

## Adding a tenant-scoped table

```sql
CREATE TABLE invoices (
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- …
);

ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoices FORCE ROW LEVEL SECURITY;

CREATE POLICY invoices_tenant_isolation ON invoices
    USING (org_id = current_tenant())
    WITH CHECK (org_id = current_tenant());
```

All four statements, every time. Then reach the table only through
`WithinTenant`, and add the cross-tenant read, write, update, and delete tests
— the ones in `internal/features/organizations` are the template.
