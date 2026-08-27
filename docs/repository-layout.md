# Repository Layout

This page describes the Nise & Go source repository, not the layout of an application it generates. The generated application layout is documented with the first generated project.

Go module path: `github.com/drilonrecica/nise-and-go` ([ADR 0007](adr/0007-module-path-and-owner.md)).

```text
cmd/nise/            CLI entry point
internal/cli/        CLI commands and presentation
internal/generator/  Deterministic generation internals
runtime/             Stable Go packages that applications import
modules/             Compile-time optional modules
templates/           Golden-profile source templates
examples/reference/  Reference business application
docs/                Public documentation and ADRs
test/                Cross-cutting fixtures and conformance tests
```

## Ownership

| Directory | Public API | Notes |
|---|---|---|
| `runtime/` | Yes | The only importable surface. Semantic versioning after `1.0`. |
| `modules/` | Yes, per module | Selected at generation time; recorded in the project recipe. |
| `cmd/`, `internal/`, `templates/` | No | Nise-private; may change in any release. |
| `examples/`, `test/` | No | Never imported by applications. |

Each directory carries a short `README.md` stating its purpose and which implementation slice populates it. Directories are created when the layout is defined, but Go packages are added only when a slice needs them.

The shape is provisional until the first vertical slice proves the package boundaries.
