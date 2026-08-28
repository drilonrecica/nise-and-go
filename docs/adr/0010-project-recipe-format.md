# ADR 0010: Project Recipe Format

- **Status:** Accepted
- **Date:** 2026-08-27

## Context

`nise new` records what it built so that `nise doctor`, `nise check`, and `nise upgrade` can act on a project they did not create. That record holds the selected profile, the selected compile-time modules, and the CLI and runtime versions that produced the project — and stops there. It is deliberately not an application configuration DSL: a file that grows settings becomes a second, competing source of truth for behavior that belongs in typed environment variables. Runtime configuration is typed environment variables (M2-001), not this file.

The file is read by a CLI that must be able to refuse a project it does not understand rather than silently misread it, and it is written by a generator that must produce byte-identical output on every machine ([ADR 0002](0002-deterministic-generation.md)).

## Decision

### File

`nise.json`, at the root of the generated project, committed to version control. Commands locate it by walking up from the working directory to the first ancestor containing it; that directory is the project root. Deleting the file is the detach step: an application detaches from Nise by removing this recipe, replacing any remaining runtime imports, and ceasing to use the CLI. There is no `nise eject` because there is nothing further to undo.

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
- `modules` holds the compile-time modules, which are a closed set: `notifications`, `organizations`, `totp`, `uploads`. No other name is valid. The array is sorted and must contain no duplicates. An empty selection is written as `[]`; the field is never omitted and is never `null`.
- `cliVersion` is the version of the `nise` binary that created or last upgraded the project.
- `runtimeVersion` is the version of `runtime/` the project targets. It is a separate field because an application may pin an older runtime in `go.mod` than the CLI that generated it.

#### Version strings

Both version fields hold a **bare version string, not a rendered banner**. `nise new` writes `internal/version.Get().Version`: a release version such as `v1.2.3`, a Go pseudo-version such as `v0.0.0-20260827120000-1a2b3c4d5e6f`, or `dev` for a source build carrying no metadata. `internal/version.Info.String()` renders `nise v1.2.3 (commit 1a2b3c4d, built 2026-08-27T00:00:00Z)`; that string must never reach the recipe, because it carries a build timestamp and the determinism rule forbids a timestamp in a generated file.

The rule the format enforces is deliberately narrow: a version is required, is at most 64 bytes, and contains only printable ASCII in the range `!`–`~`. No spaces, no control characters, no non-ASCII.

Semantic versioning is deliberately **not** required. `dev` and Go pseudo-versions are legitimate values that no released binary produces, and a recipe must still be writable from a source build.

Rejecting the space is the deliberate part rather than an accident of the character range. It is the check that stops a rendered `Info.String()`, a `v1.2.3 (dirty)` build stamp, or any other human-facing banner from being written into a field that later commands compare mechanically. A build that needs a dirty marker encodes it without a space, as semantic-versioning build metadata: `v1.2.3+dirty`.

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
| `cliVersion` or `runtimeVersion` absent, `null`, or empty | error naming the field |
| A version longer than 64 bytes | error naming the field and both lengths |
| A version containing a space, a control character, or a non-ASCII rune | error naming the field and quoting the value |
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
- `nise new` stamps the recipe from `internal/version.Get().Version`, never from `Info.String()`. Any future build stamp that wants extra detail has to fit the printable, space-free rule or move out of the recipe.
- Revisit if the recipe ever needs to carry per-module settings. That is the point at which it would be turning into a configuration DSL, and the correct answer is likely a separate file rather than a larger recipe.
