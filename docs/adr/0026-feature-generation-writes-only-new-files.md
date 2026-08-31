# ADR 0026: Feature generation writes new files and edits none

- **Status:** Accepted
- **Date:** 2026-08-31

## Context

`nise generate feature` and `nise generate resource` produce a vertical slice:
Go domain and use case, SQL, an OpenAPI contract, and Svelte pages. Most of
that is new files, which is easy. Four things are not, because they are
existing files that already belong to the application:

- `api/openapi.yaml` — the authoritative contract, hand-edited from the moment
  the project is created.
- `internal/app/app.go` — the explicit constructor wiring.
- `internal/platform/httpapi/api.go` — where an operation is implemented.
- `frontend/src/lib/navigation.ts` — the sidebar's list.

Every one of them is `[app]` under [ADR 0003](0003-application-ownership.md):
Nise writes it once and never overwrites it. So a generator that wanted to add
to them would have to *edit* them, and there are only three ways to do that.

Rewriting the file from a template discards whatever the owner wrote, which is
the exact thing ADR 0003 exists to prevent.

Inserting at a marker comment is what [ADR
0009](0009-generated-application-layout.md) already refuses: "Avoid fragile
marker-based insertion into handwritten files wherever possible." A marker is a
comment somebody deletes while tidying, moves while refactoring, or duplicates
while copying a block — and each of those turns the next generation into a
silent corruption of a file under version control.

Parsing and rewriting — a Go AST rewrite, a YAML round-trip — avoids the
marker but not the problem. `go/printer` reflows what it did not write, a YAML
round-trip loses comment placement and quoting style, and both make a
generation command something that can produce a diff the owner did not ask for
in a file they are responsible for. A tool that reformats your contract while
adding one path is a tool you stop running.

## Decision

**`nise generate` creates files. It never modifies or deletes one.**

Every artifact it produces is a new path. Where the slice needs a line in a
file the application owns, the command **prints exactly what to add and
where**, and writes nothing.

For a resource named `invoice`, that is four insertions the owner makes by
hand, each one line or a short block:

| File | What the owner adds |
|---|---|
| `api/openapi.yaml` | Two `$ref`s to the generated fragment, one per path. |
| `internal/app/app.go` | The feature's constructor call, in the existing chain. |
| `internal/platform/httpapi/api.go` | The feature's operations, delegated to its use case. |
| `frontend/src/lib/navigation.ts` | One item in the navigation list. |

The generated OpenAPI lives in its own fragment file, `api/<name>.yaml`, so
the insertion into the authoritative document is two `$ref` lines rather than
two hundred lines of schema. External `$ref` is resolved by the pinned
generator and by openapi-typescript alike.

**A generated slice is application-owned from the moment it is written.**
Nise does not regenerate it, upgrade it, or reconcile it. Running the same
command twice in the same project is refused, per file, rather than merged:
the second run stops before writing anything, naming every path that already
exists.

**Generation invents no domain rules.** A generated resource has an
identifier, one `name` column, and timestamps. Every validation, every state
transition, and every permission beyond the four CRUD ones is a `TODO` the
owner writes, because a rule a generator guessed is a rule nobody decided.

## Consequences

- The command is safe to run in a dirty working tree, on a Friday, without
  reading its source first. Its worst case is files you delete.
- Every change to a file the application owns is a change the owner made, is
  in their diff, and is reviewable as their own work.
- The cost is four manual insertions per resource. That is real, and it is
  paid once per resource by the person who is about to write the domain rules
  into the same files anyway. The command prints them in the order they are
  applied and names the file and the anchor for each.
- `nise check`'s regeneration gate is unaffected: it re-plans `nise new`'s
  output, which generated features are not part of.
- Determinism is simpler to hold than it would be for an editing generator:
  the output is a function of the name, the recipe, and the migration numbers
  already on disk, and none of those depends on the order files were written
  or on what an editor did to an existing file.
- This is revisited only if the manual insertions turn out to be a real
  obstacle in practice — measured by the reference application, not
  anticipated — and even then the alternative is a `nise generate --print`
  that emits a patch for the owner to apply, not a tool that edits their files
  itself.
