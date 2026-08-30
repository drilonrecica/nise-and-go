# `nise check`

`nise check` is the invariant runner. Run it inside a generated project — in
CI, in a pre-commit hook, or by hand — and it answers one question: does this
project still hold the properties Nise guarantees?

```sh
nise check
nise check --json
nise check --env-file deploy/production.env
```

It never writes to your project. It runs no build, starts no server, opens no
network connection, and runs exactly one subprocess: `git ls-files`, to learn
which files are tracked.

## What an invariant is, and what it is not

The project draws this line explicitly:

> `nise check` enforces safety, compatibility, migration, and generation
> invariants. It does not enforce every starter folder, dependency,
> component, or style decision after customization.

Getting that boundary right is the whole design of this command. The rule
every check here is admitted under:

> A check belongs in `nise check` only if a violation breaks a property Nise
> itself guarantees — through `runtime/`, through the recipe, or through the
> ownership and determinism rules of generation — and that an application
> cannot legitimately opt out of by editing a file it owns.

The corollary carries as much weight as the rule. When a property is enforced
by a file the *application* owns and may rewrite — `internal/platform/config/
config.go`, the router's middleware list, the frontend's component tree —
`nise check` must not assert it. See [Checks that were deliberately
excluded](#checks-that-were-deliberately-excluded).

**One check is admitted on a different ground, and it is worth naming.**
`secret-material` does not come from `runtime/`, the recipe, or the rules of
generation. It rests on Global Constraint 8 — no secret, token, cookie value,
password, or connection-string password may surface — extended to the
project's own tracked files. It is also the one check that *can* fail a
project whose only fault is that it was customized, which is exactly the case
the rule above exists to exclude. What keeps the exception defensible is that
the property is not a Nise convention at all: a committed credential is a
defect in any repository under any framework, and is the one finding whose
cost of being missed is unbounded. The price is a real false-positive risk,
which is why every rule below is written narrow, is paired with an explicit
tolerance, and has both a detection and a non-detection test.

## `skipped` is a first-class result

A check reports `skipped`, always with a reason, whenever it cannot actually
verify its invariant: the subsystem does not exist yet, an input was not
supplied, or a difference could not be told apart from a legitimate one.

This is not politeness. `nise doctor` shipped a version that reported `fail`
for the `sqlc`/`goose`/`oapi-codegen` tool directives that only the framework
repository declares. Three things made it a real defect: it fired in the
first context a user runs the command, it exited non-zero so a CI gate failed
on a healthy project, and **the remedy it printed was actively wrong** — it
told users to add tool directives to their application's `go.mod`. A wrong
remedy is worse than a bare failure. Wherever `nise check` would have to
guess, it reports what it saw, says why it cannot conclude, and does not fail
the run.

A `skipped` check is never counted as a pass and never fails the run.

## Exit codes

Follows [the CLI output contract](../cli-output.md#exit-codes).

| Code | When |
|---|---|
| 0 | Every check that could be evaluated passed. Skipped checks do not change this. |
| 1 | At least one check reported `fail`. Also the code for `--fix`, which is not implemented. |
| 2 | The command line was invalid (an unknown flag, `--quiet` with `--verbose`). |
| 3 | No `nise.json` was found in the working directory or any parent. |

Exit 3 outside a project is deliberate. Every check this command runs is
about a project; reporting nine skips and exiting 0 would be a green result
that verified nothing.

## Output shape

Human mode prints one block per check — status tag, the invariant it
protects, what was observed, the offending paths, and the remedy — then a
`N ok, N fail, N skipped` summary. The invariant line is printed for every
status including `skipped`, because a reader of a skipped check has to be
told what went unverified.

`--json` emits one JSON document per line
([NDJSON](../cli-output.md#json-mode)), one per check, plus a final
[error envelope](../cli-output.md#json-rendering) when the run failed. The
per-check schema is stable; adding a field is backward compatible, renaming
or removing one is a breaking change:

```json
{
  "name": "generation-deterministic",
  "status": "fail",
  "invariant": "Regenerating this project's nise-owned files reproduces them byte for byte (ADR 0002).",
  "detail": "1 of 6 nise-owned file(s) do not match what nise v0.2.0 generates: internal/app/modules.gen.go differs from what nise generates (first difference at line 12)",
  "remedy": "Restore the listed files from version control. …",
  "paths": ["internal/app/modules.gen.go"]
}
```

`name`, `status`, `invariant`, and `detail` are always present. `remedy` is
present on every `fail`, and on a `skipped` check only when there is a
genuinely correct action. `paths` is present only for checks about particular
files. `status` is one of `ok`, `fail`, `skipped` — there is no `warn`:
`nise doctor` has one because it reports on a machine, where "your Docker is
older than we test against" must not fail a build; `nise check` reports on a
project in CI, and a finding nobody is obliged to act on and nothing is
obliged to gate on is how a check quietly stops meaning anything.

The checks are always evaluated in the order below, so the JSON stream's
order is stable across runs and machines.

## The checks

### `recipe`

**Invariant:** the project declares a recipe this build of nise understands
([ADR 0010](../adr/0010-project-recipe-format.md)).

Loads `nise.json` through `internal/recipe` — the package that owns the
format — so schema version, profile, module names, and version fields are all
validated by the same code that writes them.

**Fails** when the file does not parse, names an unknown profile or module,
or declares a schema version this build does not support. The remedy is
version-aware: a recipe from a *newer* schema needs an upgraded nise, not a
hand repair, and telling that user to edit the file would be the wrong
remedy.

When this check fails, every check that depends on a valid recipe reports
`skipped` rather than piling four more failures onto one broken file.

### `cli-version-compatibility`

**Invariant:** the running nise is not older than the nise that generated the
project.

Running a *newer* nise is fine and expected. Running an *older* one is a real
compatibility violation: this binary's templates, recipe schema, and
ownership rules are the older ones, so any regeneration it performs is a
downgrade of files it does not fully understand.

**Skips** when either version string carries no comparable version number — a
from-source build reports `dev`, and claiming `ok` without having compared
anything is exactly the dishonest status this command avoids.

Note that a recipe's `cliVersion` and `runtimeVersion` legitimately differ:
`nise new` stamps `runtimeVersion` from the framework module version the
generated `go.mod` requires, and `cliVersion` from the binary's own version.
This check compares `cliVersion` only.

### `runtime-version-consistency`

**Invariant:** the recipe's `runtimeVersion` is the runtime version `go.mod`
actually requires.

A recipe naming a runtime the project does not use is a recipe that lies
about the project, and every later compatibility decision — including the
upgrade path milestone M6 builds — reads that field rather than `go.mod`.

**Skips** when `go.mod` is absent, unreadable, or states no single `require`
for the Nise module. A project that no longer requires the Nise module at all
is *not* a failure. Detaching from Nise means removing the project recipe,
replacing any remaining runtime imports, and ceasing to use the CLI — there
is no `nise eject` because there is nothing further to undo — and a project
part-way through that is not broken.

The `go.mod` reader is a deliberately narrow line scanner, not a `go.mod`
parser: `golang.org/x/mod` would be this module's first runtime dependency
([Global Constraint 2](../dependencies.md)) for one lookup whose only
consumer skips the moment anything is ambiguous. It can under-report; by
construction it cannot mis-report.

### `ownership-headers`

**Invariant:** every generated file still declares the ownership model it was
written with ([ADR 0003](../adr/0003-application-ownership.md)).

The two directions are deliberately asymmetric, and the asymmetry *is* the
boundary this command is about:

- A **nise-owned** file must still carry `Code generated by nise; DO NOT
  EDIT.` within its first five lines. Losing it is unambiguous evidence of a
  hand edit, and — unlike the regeneration check — the evidence stays
  conclusive no matter which nise built the file, because the header records
  no version.
- An **application-owned** file must merely *not* carry that header. Its
  absence proves nothing: the application owns the whole file and may delete
  the courtesy header, reorganize the file, or rewrite it from scratch.
  Requiring the app-owned header to survive would enforce starter conformity
  on a file whose entire point is that the application decides. What this
  direction catches is the one thing that is a real violation: generated
  output having landed on top of a file the application owns.

A generated file that is simply **gone** is not a violation either. Deleting
an application-owned file is the application's prerogative, and a deleted
nise-owned file is caught, with a more useful message, by the regeneration
check.

`nise.json` is exempt: JSON has no comment syntax to hold a header. Its
integrity is covered twice over by the `recipe` and
`generation-deterministic` checks.

The five-line window is not arbitrary. Line 1 carries the header for a Go
file or a Markdown comment; line 2 for the embedded HTML placeholder, whose
doctype must come first. A whole-file substring scan would misread the
generated `README.md`, which quotes the marker verbatim while documenting
these very rules.

### `ownership-content-hash`

**Invariant:** the nise-owned files that carry their own content hash still
verify against it.

`AGENTS.md` and `.nise/architecture.json` embed a SHA-256 of their own body
at generation time (the same scheme `nise agents` uses to refuse to overwrite
a hand-edited file). This is the one hand-edit check that is **conclusive
regardless of which nise generated the project**, because the hash covers the
file's own body rather than comparing it against what today's templates would
produce.

The verification is reached through `internal/agentsfile` rather than
recomputed here, so the rule stays defined in exactly one place: the two
files are copied into a temporary directory and `agentsfile.Write` is aimed
at *the copy*. Your project is never written to.

### `generation-deterministic`

**Invariant:** regenerating this project's nise-owned files reproduces them
byte for byte ([ADR 0002](../adr/0002-deterministic-generation.md)).

The project described by the recipe is regenerated into a temporary
directory, and every nise-owned file is compared byte for byte against what
is on disk. Differing paths are reported individually, with the line number
of the first difference. The temporary directory is removed before the
command returns.

The whole tree is written out rather than compared in memory because the
invariant is about *generation*, not rendering: path separators, directory
creation, and the write itself are part of what has to be reproducible.

**This is the check with the sharpest wrong-remedy hazard in the command**, and
it is the one place `nise check` deliberately declines to conclude. A
difference has two possible causes it cannot tell apart:

1. somebody edited a nise-owned file, or
2. the project was generated by a different nise whose templates have since
   changed.

Only the first is an invariant violation, and only the first has "restore the
file" as its remedy. Telling a developer who simply upgraded their CLI that
they hand-edited a file, and to throw their work away, is the wrong-remedy
defect described above.

So:

- **Same nise** as the recipe records → the comparison is conclusive, and a
  difference is a `fail`.
- **Different nise** → the comparison still runs and the differing paths are
  still reported, but as a `skipped` with the honest reason. Hand edits are
  not thereby unguarded: `ownership-headers` and `ownership-content-hash`
  cover them independently of version.

Versions are compared *parsed*, not as raw strings. A binary built from a
dirty source checkout reports the recipe's own pseudo-version with `+dirty`
appended — the same build, the same templates, a different string — and
comparing strings there would silently downgrade this check to a skip for the
person most likely to be running it.

Because no nise-owned template references the application's name or module
path (a test in `internal/check` asserts this and fails the build if it ever
stops being true), the regeneration does not depend on recovering them
exactly, and renaming a project directory does not produce phantom
differences.

### `migrations`

**Invariant:** database migrations are ordered, forward-only, and every
applied migration is still present.

**`skipped` by this offline command.** Generated projects contain readable
embedded Goose migrations, an instance-scoped runner, and a read-only
compatibility check, but `nise check` has no selected database endpoint.
Run [`nise db status`](db.md) against the intended database. Reporting `ok`
without doing that would claim deployed-history guarantees that were never
verified.

### `config-invariants`

**Invariant:** an environment file the application would start from breaks no
configuration rule `runtime/` enforces unconditionally.

**Skipped unless `--env-file` names a file.** There is deliberately no
default: guessing at `.env` would validate a developer's local file as though
it were the production one and report on the wrong thing.

Given a file, it validates production configuration **without building,
starting, or importing the application**, using the runtime's own parsers so
that a change to what the runtime accepts changes what this check accepts:

| Rule | Enforced by |
|---|---|
| `APP_ENV` is one of `development`, `test`, `production` | `runtime/config.ParseEnvironment` — a closed set with no default and no fourth value |
| `APP_MODE` is one of `all`, `web`, `worker` | `runtime/lifecycle.ParseMode` |
| No variable is supplied as both `NAME` and `NAME_FILE` | `runtime/config.Loader.Secret`, unconditionally, in every environment |
| A `NAME_FILE` target that exists on this machine is not readable by group or other | `runtime/config`'s own secret-file permission rule (skipped on Windows, where those bits do not apply — the same exemption `runtime/config` makes) |
| Every non-blank, non-comment line is a `KEY=VALUE` assignment | This check: a file it cannot parse is reported, not guessed at |

The accepted dialect is the small one: blank lines and `#` comments are
ignored, an optional leading `export ` is allowed, a value in matching single
or double quotes has them stripped, an unquoted value ends at the first ` #`,
and a repeated key takes its last value. Escape sequences, multi-line values,
and variable interpolation are not implemented.

**Fails** when a rule above is broken, or when `--env-file` names a file that
cannot be read — the user asked for a check that then did not happen, and
must be told.

What this check pointedly does *not* do is re-implement the application's own
production validation; see the next section.

### `secret-material`

**Invariant:** no secret material is committed in the project's tracked files
([Global Constraint 8](../security.md)).

Git's own answer to "which files are tracked" is used when available —
`git ls-files`, which needs no reimplementation of `.gitignore` semantics. A
generated project is not a git repository until you make it one (`nise new`
deliberately runs no git), so when there is no repository **every file in the
tree is scanned instead**, minus a short fixed list of dependency and build
directories (`.git`, `node_modules`, `.svelte-kit`, `dist`, `build`,
`vendor`), and the check says which of the two it did. Reporting "skipped:
not a git repository" would put the check's most valuable moment — the
freshly generated project — out of reach.

Five rules, each chosen to be high-confidence rather than broad, because a
security check that fails a correct project in CI is a check that gets
disabled:

| # | Rule | Tolerance |
|---|---|---|
| 1 | A committed plaintext environment file (`.env`, `.env.<anything>`) | Two families are exempt: the **example** forms (`.env.example`, `.env.sample`, `.env.template`, `.env.dist`), and the **encrypted** forms — any `.env…` name ending `.age`, `.asc`, `.enc`, `.encrypted`, `.gpg`, `.pgp`, `.sealed`, `.sops`, or `.vault` |
| 2 | A file named or suffixed like a private key (`id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519`, `*.key`, `*.p12`, `*.pfx`, `*.jks`, `*.keystore`) | `*.pem` is deliberately **not** in this list — see below |
| 3 | A `-----BEGIN … PRIVATE KEY-----` armor block, in any text file | Binary files (any NUL byte) and files over 1 MiB are not scanned |
| 4 | A secret-named variable (`*SECRET*`, `*TOKEN*`, `*PASSWORD*`, `*PASSWD*`, `*API_KEY*`/`*APIKEY*`, `*PRIVATE_KEY*`, `*CREDENTIAL*`) assigned a real value, in a file whose format is `KEY=VALUE`. The `*` means *any affix, including none*: bare `TOKEN=`, `SECRET=`, and `API_KEY=` match | Only in `.env`-shaped files, so `token := cfg.OperatorToken` in Go source is not a finding; **encrypted** env files are skipped (their values are ciphertext envelopes) but **example** files are not, since a real credential pasted into `.env.example` is one of the likeliest ways a secret gets committed; values under 8 characters, and conventional placeholders (`changeme`, `your-…`, `<…>`, `${…}`, empty) are markers, not material |
| 5 | A URL carrying an inline password (`postgres://user:pass@host`) | A password equal to its own username, or matching a placeholder (`postgres://user:password@…` in prose), or under 8 characters, is a throwaway local credential |

The remedy names rotation, not only deletion: removing a committed secret in
a new commit leaves it readable in every earlier one.

**Why encrypted environment files are exempt.** Committing an encrypted
environment file is correct practice — it is how a team keeps configuration
in version control without keeping secrets in it. Failing a project for a
`.env.enc`, `.env.sops`, or `.env.age`, with a remedy telling the operator to
rotate every credential they just carefully encrypted, is exactly the "punish
a legitimately customized project" failure the admission rule at the top of
this page exists to prevent — and with no suppression mechanism in this
slice, such a project could never make `nise check` green at all. Recognizing
the extensions was chosen over adding a suppression flag or ignore file: a
suppression mechanism is a general escape hatch that also silences findings
nobody examined, and would need a format, a location, and documentation of
its own. The trade-off, stated plainly: a file named `.env.enc` that was
never actually encrypted is taken at its word. That is the same trust the
check already places in `.env.example`, and the alternative — telling
ciphertext from plaintext by inspection — is guesswork that fails in both
directions.

**Why `*.pem` is not a private-key extension.** PEM is an armor *encoding*,
not a key format. A `.pem` file is just as likely to be a public certificate
or a CA bundle, and `deploy/tls/ca.pem` holding nothing but
`-----BEGIN CERTIFICATE-----` is a correct thing to commit. Rule 3 already
finds a real private key inside any text file by its armor header, whatever
the file is called, so listing the extension in rule 2 would add false
positives and no detection.

## Checks that were deliberately excluded

Each of the following would have been easy to add, would have caught real
mistakes in the starter as generated, and is **excluded on purpose** because
it enforces conformity to the starter rather than an invariant. In every case
the property is enforced by a file the application owns and may legitimately
replace — so a correct-but-customized project would fail, and the printed
remedy would be advice to undo a decision the application was entitled to
make.

### 1. `DEBUG=false` in production

The generated `internal/platform/config/config.go` refuses to start a
production process with `DEBUG=true`, and the generated `.env.example`
documents it. Tempting, and genuinely valuable — but that rule lives in an
**application-owned** file ([ADR 0003](../adr/0003-application-ownership.md)).
If the application still has that enforcement, the process fails at startup
anyway and `nise check` catching it early is a convenience, not an invariant.
If the application removed or repurposed `DEBUG`, failing here is wrong.
`internal/check` has a test (`TestConfigCheckDoesNotEnforceApplication
Policy`) that asserts this exclusion, so a future change has to confront the
ownership question rather than quietly re-add the check.

### 2. `LOG_FORMAT=json` in production

Same file, same ownership, and weaker still: machine-readable production logs
are an operational preference, not a safety property. An application shipping
its own log pipeline may choose otherwise.

### 3. The `BIND_ADDR` / `ALLOW_PUBLIC_BIND` pairing

The starter refuses a wildcard bind in production without an explicit opt-in.
That is a good default and a deployment-topology decision — which network an
application listens on depends on what is in front of it. It is enforced by
the same application-owned file, and an application that replaced the policy
(bound to a specific interface list, or delegated the decision to its
orchestrator) is not broken.

### 4. Starter directory and file presence

`nise check` does not verify that `internal/features/`, `api/openapi.yaml`,
`deploy/compose.yaml`, or any other application-owned path still exists. Generated
application components are owned by the application and may be edited,
replaced, or removed, and the ownership check already treats a deleted
application-owned file as allowed. The generated project's `make api-check`
is the opt-in contract-specific gate: when the application keeps the starter's
OpenAPI boundary, it compares the checked-in strict bindings to
`api/openapi.yaml` without making `nise check` require that boundary forever.

### 5. The dependency set

`nise check` does not verify that `go.mod` still requires chi, or that
`frontend/package.json` still pins the versions `nise new` wrote. Applications
may directly add dependencies Nise does not use: the Nise dependency
allowlist governs generated and maintained framework code, not application
owners ([Dependency allowlist](../dependencies.md)). The one dependency
fact that *is* checked —
`runtime-version-consistency` — is checked because the recipe claims it, not
because the starter chose it.

### 6. Middleware order in the generated router

`internal/platform/httpapi/router.go` is application-owned, and applications
may deliberately change its contract. The generated starting point exposes
separate API and document middleware slots inside a fixed protected core and
locks the behavior in an application-owned router test ([API routing and
middleware](../api-routing.md)). `nise check` does not text-inspect or rewrite
that application-owned code: if an application changes the boundary, it owns
the corresponding security reasoning and test update.

## `--fix` is not implemented

The flag is defined so that typing it produces an honest answer instead of
"flag provided but not defined". It always errors, with exit 1 and the
machine-readable code `check.fix_not_implemented`, and changes nothing:

```
$ nise check --fix
nise check --fix is not implemented in this version
Run "nise check" and apply the remedy each failing check prints. …
```

Every remedy `nise check` prints is a command or an edit you perform
yourself. `nise check` never writes to your project.

## Running it in CI

```yaml
- name: nise check
  run: nise check --json
```

Exit 0 is the gate. Use `--json` in CI: it is one document per line, it never
emits ANSI, and the `name` field of each document is stable across releases,
so a job can key off a specific check without parsing prose.

## Related

- [`nise doctor`](doctor.md) — checks the *machine's* tooling; `nise check`
  checks the *project's* invariants.
- [`nise agents`](agents.md) — regenerates the two self-verifying nise-owned
  files that `ownership-content-hash` verifies.
- [CLI output contract](../cli-output.md) — output modes, exit codes, the
  JSON envelope.
- [Generated application layout](../generated-application-layout.md) — the
  ownership markers these checks read.
