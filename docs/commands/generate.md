# `nise generate`

`nise generate` scaffolds a vertical slice into an existing application: a
feature's package, its use case with the transaction boundary and the
permission check, and — for a resource — the SQL, the contract, and the
frontend flows around it.

```
$ nise generate feature invoice
$ nise generate resource Order
$ nise generate feature --help
```

See [CLI output contract](../cli-output.md) for `--json`, `--verbose`,
`--quiet`, and the exit-code contract in general.

## Subcommands

| Subcommand | Scaffolds |
|---|---|
| `nise generate feature <name>` | A new vertical-slice feature under `internal/features/<name>/` — domain rules, use case, handler, SQL queries, and tests, per [Generated application layout](../generated-application-layout.md). |
| `nise generate resource <name>` | A full CRUD resource (typically within a feature): the same vertical slice as `feature`, populated with the create/read/update/delete surface a REST collection needs. |

Both take exactly one positional argument: the name. Passing zero
arguments, or more than one, is a usage error (exit `2`) before any
generator is even consulted.

## Naming rules

A name must:

- Start with a letter and contain only letters and digits — no
  underscore, hyphen, or space. [Generated application
  layout](../generated-application-layout.md#naming-rules) requires this
  twice over: "Go directory names contain no underscores or hyphens", and
  the name becomes the feature's Go package name, which the same rules
  require to match the directory.
- Be at most 64 characters.
- Not be one of Go's own reserved keywords (`type`, `func`, `range`, …) —
  the name becomes a Go package and type name.
- Not be one of the three core-service feature names [ADR
  0009](../adr/0009-generated-application-layout.md) reserves for
  `nise new`: `auth`, `authz`, `audit`.
- Not be one of the generic directory names ADR 0009 explicitly excludes
  from `internal/`: `util`, `common`, `shared`, `pkg`.
- Not be one of the ownership-marking directory names [Generated
  application
  layout](../generated-application-layout.md#ownership-markers) defines:
  `store`, `openapigen`, `embedded`. A feature or resource sharing one of
  these names would collide, confusingly, with the ownership signal the
  path is supposed to carry.

The last four checks are case-insensitive. A leading-capital input (for
example `Order`) is accepted and lowercased to its canonical directory
form (`order`) rather than rejected — it is the natural spelling of the
Go exported type the eventual generator will also write, and requiring a
caller to already know to lowercase it first would be a needless trap.

A name that fails any of these rules is rejected with a usage error (exit
`2`) explaining exactly which rule it broke, in both human and `--json`
mode — before any generator is invoked.

```
$ nise generate feature invoice_line
name "invoice_line" must start with a letter and contain only letters and digits — no underscore, hyphen, or space (docs/generated-application-layout.md: "Go directory names contain no underscores or hyphens")
Choose a name matching ^[A-Za-z][A-Za-z0-9]*$ that is not a reserved word.
```

## What it writes, and what it refuses to write

`nise generate` **creates files and modifies none** ([ADR
0026](../adr/0026-feature-generation-writes-only-new-files.md)).

```
$ nise generate feature invoice
Created feature "invoice" — 4 files
  internal/features/invoice/README.md
  internal/features/invoice/domain.go
  internal/features/invoice/invoice_test.go
  internal/features/invoice/usecase.go

Now add these, in this order. nise does not edit files it did not
write, so every change to a file you own stays in your own diff.

internal/platform/authorization/catalog.go
  the permission block, the permissions() list, and the administrator role

    // InvoicesRead permits reading invoices.
    InvoicesRead = authz.MustPermission("invoices.read")
    ...
```

A vertical slice needs a line in files that already belong to the
application — the permission catalog, the constructor wiring, the OpenAPI
document, the navigation list. Rewriting one would discard what its owner
put there; inserting at a marker comment is what [ADR
0009](../adr/0009-generated-application-layout.md) refuses; and rewriting
through a parser reformats a file its owner is responsible for. So the
command prints exactly what to add and where, and writes nothing.

Everything it does write is **application-owned from the moment it is
written**. Nise does not regenerate it, upgrade it, or reconcile it.

## Running it twice

A second run is refused, per file, and writes nothing:

```
$ nise generate feature invoice
feature "invoice" already exists; nothing was written
These paths are already there:
  internal/features/invoice/README.md
  internal/features/invoice/domain.go
  internal/features/invoice/invoice_test.go
  internal/features/invoice/usecase.go

Choose another name, or remove them if they are no longer wanted. A generated
slice is yours from the moment it is written, so nise will not replace one.
```

Every path is checked before anything is written, so a colliding run leaves
the project exactly as it found it — no half-written feature to identify and
remove by hand. The write itself uses `O_EXCL`, so even a file created between
the check and the write is refused rather than overwritten, and a path already
held by a *directory* is refused the same way rather than failing halfway
through with "is a directory".

Three properties are pinned by tests rather than promised:

- A run creates exactly the paths it planned and changes nothing else. The
  test builds a project containing the files a careless generator might
  plausibly want to edit — the OpenAPI document, `app.go`, the permission
  catalog, the navigation list — hashes the whole tree, generates, and
  compares.
- A refused second run changes nothing at all, including a file you have since
  edited. That is the case that actually happens: somebody runs the command
  twice by accident after making the generated code their own.
- No insertion the command prints names a file the command wrote. An insertion
  into a generated file would mean the generator should have written that line
  itself.

So the worst a wrong invocation can do is create files you delete.

## What it does not decide

Generation invents no domain rules. A generated resource has an identifier, a
`name`, and timestamps; every validation, state transition, and permission
beyond the two it checks is a `TODO`, because a rule a generator guessed is a
rule nobody decided.

## Exit codes

| Situation | Exit code |
|---|---|
| Wrong number of positional arguments | `2` (`ExitUsage`) |
| Name fails a naming rule | `2` (`ExitUsage`), code `generate.invalid_name` |
| Not run from the root of a Go project | `1` (`ExitError`), code `generate.not_a_project` |
| The slice already exists | `1` (`ExitError`), code `generate.exists` |
| Success | `0` |
