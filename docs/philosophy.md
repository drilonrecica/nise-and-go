# Philosophy

## Rails-like experience, Go-like implementation

Nise borrows Rails' decisiveness: a clear project shape, generators, complete workflows, and a small number of commands. It does not copy Rails' runtime architecture.

Nise applications should remain recognizable Go:

- Explicit constructors and dependencies.
- Normal packages and interfaces.
- SQL visible in the repository.
- No reflection-driven dependency container.
- No active-record abstraction.
- No hidden transaction boundaries.
- No mandatory runtime service beyond PostgreSQL.

## One golden path

Broad configurability transfers maintenance cost to every user. Nise instead supports one coherent V1 profile deeply. A future profile must be designed, documented, and tested as a complete product choice.

## Deterministic generation

Given the same Nise version and recipe, generators must produce the same result. Generation must work without AI, external accounts, or model APIs. CI regenerates owned files and fails when the result differs.

## Application ownership

Generated business code and frontend components belong to the application. Developers may edit or replace them. Nise enforces safety and compatibility invariants, not permanent visual or structural conformity.

## Security and performance

Security-first means defaults fail closed, dangerous behavior is explicit, and threat assumptions are documented. It does not mean vulnerabilities are impossible.

Performance is measured through representative workloads, allocations, memory, startup, binary size, frontend transfer size, and database behavior. Marketing follows evidence rather than preceding it.

## Escape hatches

Applications may use direct pgx, add dependencies, introduce custom feature packages, replace generated UI, and eventually stop using Nise. A framework that requires a complicated eject operation is too invasive.

