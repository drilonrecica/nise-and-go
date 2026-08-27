# `nise generate`

`nise generate` is the command surface for the feature and resource
generators [milestone M7](../roadmap.md) will build. **As of this task
(M1-014), no generator exists.** This command's argument parsing and name
validation are genuine today; running a subcommand always fails with an
honest error naming the milestone that will implement it. It never
fabricates a success.

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
Choose a name matching ^[A-Za-z][A-Za-z0-9]*$ that is not a reserved word; see "nise generate feature --help".
```

## What happens once the name is valid

Every subcommand delegates to a `Generator` — a small interface
(`internal/cli/generate.go`) that milestone M7 implements. This build
wires in the only implementation that exists today, one whose methods do
no work and touch no filesystem: they always return an honest
`*NotImplementedError` naming the subcommand and the milestone that adds a
real one. `nise generate`'s Run function turns that into the command's own
error:

```
$ nise generate resource Order
nise generate resource is not implemented in this version (arrives with milestone M7)
Write resource "order" by hand for now, following docs/generated-application-layout.md, or track milestone M7 in the project roadmap.
```

```
$ nise generate resource Order --json
{"error":{"code":"generate.not_implemented","docs":"docs/commands/generate.md","message":"nise generate resource is not implemented in this version (arrives with milestone M7)","recovery":"Write resource \"order\" by hand for now, following docs/generated-application-layout.md, or track milestone M7 in the project roadmap."}}
```

**This is by design, not a placeholder bug.** A command that faked
success here would tell a user (or a script driving `nise generate` in
CI) that a feature exists when nothing was written — silently corrupting
whatever depended on that feature actually being there. Exit code `1`
(`ExitError`) and an honest message are the only acceptable outcome until
M7 lands.

## Exit codes

| Situation | Exit code |
|---|---|
| Wrong number of positional arguments | `2` (`ExitUsage`) |
| Name fails a naming rule | `2` (`ExitUsage`), code `generate.invalid_name` |
| Name is valid, but no generator implementation exists yet (today, always) | `1` (`ExitError`), code `generate.not_implemented` |
| A future M7 `Generator` implementation itself fails | `1` (`ExitError`) |
| A future M7 `Generator` implementation succeeds | `0` |

## The delegation seam, for a future M7 implementer

```go
// Generator is the seam the real M7 generators implement.
type Generator interface {
	GenerateFeature(ctx context.Context, root, name string) error
	GenerateResource(ctx context.Context, root, name string) error
}
```

`generateCommand()` in `internal/cli/generate.go` is the only place that
constructs a `Generator` and wires it into the two subcommands. M7 adding
a real implementation is expected to be a change to that one wiring line
— replacing `notImplementedGenerator{}` with a real type — not a rewrite
of this command's flag parsing, help text, or name validation, all of
which are already correct and already tested.
