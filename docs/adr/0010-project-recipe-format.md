# ADR 0010: Project Recipe Format

- **Status:** Accepted
- **Date:** 2026-08-27

## Context

`nise new` records what it built so that `nise doctor`, `nise check`, and `nise upgrade` can act on a project they did not create. `BLUEPRINT.md` §12 and §17 require that record to hold the selected profile, the selected compile-time modules, and the CLI and runtime versions that produced the project — and to stop there, without becoming an application configuration DSL. Runtime configuration is typed environment variables (M2-001), not this file.

The file is read by a CLI that must be able to refuse a project it does not understand rather than silently misread it, and it is written by a generator that must produce byte-identical output on every machine ([ADR 0002](0002-deterministic-generation.md)).

## Decision

### File

`nise.json`, at the root of the generated project, committed to version control. Commands locate it by walking up from the working directory to the first ancestor containing it; that directory is the project root. Deleting the file is the detach step described in `BLUEPRINT.md` §5.

JSON is the format because `encoding/json` is in the standard library, and the framework's dependency target for M0–M2 is zero runtime dependencies. TOML and YAML are excluded for that reason alone; both would read better with comments.

### Schema

```json
{
  "schemaVersion": 1,
  "profile": "go-chi-postgres-svelte",
  "modules": [
    "notifications",
    "organizations",
    "totp",
    "uploads"
  ],
  "cliVersion": "v0.1.0",
  "runtimeVersion": "v0.1.0"
}
```

Every field is required. Field order is fixed as written above, indentation is two spaces, and the document ends with a newline.

- `schemaVersion` is an integer, currently `1`. It exists so a future CLI can reject or migrate an unknown recipe instead of misreading it.
- `profile` names the golden profile from [ADR 0001](0001-golden-profile.md). `go-chi-postgres-svelte` is the only accepted value; the name describes the stack so a later profile can be added beside it rather than renumbering.
- `modules` holds the compile-time modules from `DECISIONS.md`: `notifications`, `organizations`, `totp`, `uploads`. No other name is valid. The array is sorted and must contain no duplicates. An empty selection is written as `[]`; the field is never omitted and is never `null`.
- `cliVersion` is the version of the `nise` binary that created or last upgraded the project.
- `runtimeVersion` is the version of `runtime/` the project targets. It is a separate field because an application may pin an older runtime in `go.mod` than the CLI that generated it.

### Excluded from the recipe

- The application name and Go module path. `go.mod` is authoritative; duplicating it invites drift.
- Anything an environment variable configures: ports, database URLs, secrets, log levels, feature flags.
- Timestamps, hostnames, usernames, and absolute paths, per the determinism rule.
- Free-form user fields. Unknown fields are an error, not an extension point.

### Behavior

`internal/recipe` implements the format. `Marshal` validates before it encodes and canonicalizes as it encodes: modules are sorted, HTML escaping is off, and the byte output is a pure function of the recipe's values. `Unmarshal` is strict — `DisallowUnknownFields`, no trailing content after the document, required fields distinguished from zero values — and returns the canonical sorted form so that `Unmarshal(Marshal(r))` equals `r` for every valid recipe.

Errors name the offending field and, where a closed set applies, list the accepted values:

| Input | Result |
|---|---|
| Unknown field | error naming the field |
| `schemaVersion` greater than the supported version | error naming both versions and directing the reader to upgrade `nise` |
| `schemaVersion` less than 1, or absent | error naming the field |
| Unknown profile | error listing accepted profiles |
| Unknown module | error naming `modules[i]` and listing accepted modules |
| Duplicate module | error naming the duplicate |
| `modules` absent or `null` | error directing the reader to `[]` |
| Empty file | error stating the recipe is empty |
| Malformed JSON | wrapped syntax error with its byte offset |
| JSON array or scalar at the top level | error stating the recipe must be a JSON object |

Because `Marshal` is the canonicalizer, `nise check` can assert that the committed `nise.json` is byte-identical to `Marshal` of its own parsed contents, which catches hand edits that changed formatting or module order.

## Consequences

- A hand-edited recipe with a typo fails loudly at the first command that reads it, rather than silently disabling a module.
- The recipe carries no comments, so the reason a module was chosen lives in the project's own documentation. This is the accepted cost of a standard-library-only format.
- Adding a field later requires either an optional field at `schemaVersion` 1 or a bump to 2 with a migration in `nise upgrade`. Removing or retyping a field always requires a bump.
- Older CLIs reject newer recipes by design. The error must say which CLI version to install; that message is a compatibility surface.
- `cliVersion` changes on every CLI release, so golden tests over generated trees must inject a fixed version rather than read the build's own.
- Revisit if the recipe ever needs to carry per-module settings. That is the point at which it would be turning into a configuration DSL, and the correct answer is likely a separate file rather than a larger recipe.
