# `nise new`

`nise new` creates a new application from the golden profile: one Go binary
that serves an embedded SvelteKit application, with typed configuration,
structured logging, request and correlation IDs, health probes, graceful
shutdown, process modes, security headers, and metrics already wired from
the `runtime/` packages.

```
$ nise new myapp
$ nise new myapp --module-path github.com/you/myapp --yes
$ nise new myapp --module organizations --module totp --no-input
$ nise --json new myapp --module-path github.com/you/myapp
```

See [CLI output contract](../cli-output.md) for `--json`, `--verbose`,
`--quiet`, and the exit-code contract.

## What it does, and what it deliberately does not

Generation **writes files and nothing else**. It opens no network
connection, starts no subprocess, and runs no tool — not `go mod tidy`, not
`go get`, not `pnpm install`, not `git init`. The exact commands to run next
are printed instead, so every network-touching step is one the user typed.

```
Next:
  cd myapp
  go mod edit -replace github.com/drilonrecica/nise-and-go=/path/to/your/nise-and-go
  go mod tidy
  pnpm --dir frontend install
  pnpm --dir frontend build
  go run ./cmd/myapp

Note:
  Pre-release: github.com/drilonrecica/nise-and-go v0.1.0 is not published yet,
  so `go mod tidy` cannot resolve it without the replace directive above.
  Point it at a local checkout of the framework. Once v0.1.0 is
  tagged and on the module proxy, drop that command and delete the replace
  line it wrote into go.mod.
```

The generated `go.mod` therefore carries pinned `require` lines and no
`go.sum` (see [the pinned framework version](#the-pinned-framework-version-is-not-published-yet) for the one caveat that applies to it today), and the generated `package.json` carries exact versions (never a
range) and a `packageManager` field, so `go mod tidy` and `pnpm install`
resolve to exactly what generation intended.

## The pinned framework version is not published yet

`nise new` writes `require github.com/drilonrecica/nise-and-go v0.1.0` into
the generated `go.mod`. **No tagged release of that module carries the
`runtime/` packages yet**, so in a fresh project `go mod tidy` fails:

```
go: github.com/drilonrecica/nise-and-go@v0.1.0: reading .../go.mod at revision v0.1.0: unknown revision v0.1.0
```

This is a pre-alpha condition, not a permanent workflow. Until the first
tagged release, a generated project needs one extra command pointing the
module at a local checkout of the framework:

```sh
go mod edit -replace github.com/drilonrecica/nise-and-go=/path/to/your/nise-and-go
```

The command is printed by `nise new` itself, in both human and `--json`
output (`nextCommands` and `notes`), and the generated `README.md` opens
with a "Before the first build" section stating the same thing. It is
deliberately duplicated in all three places: a first step that is known to
fail must not be presented anywhere as one that works.

The path in every one of those places is the literal placeholder
`/path/to/your/nise-and-go`, never a resolved path — an absolute path in
generated output would be machine-specific, which the determinism rule
forbids.

When `v0.1.0` is tagged and on the module proxy, the replace line is deleted
from `go.mod` and nothing else about a generated project changes. Publishing
that tag belongs to the release milestone; `nise new` will drop the extra
command and this section at the same time.

Why the version is pinned rather than resolved: generation performs no
network access, so it cannot ask what the newest version is, and a version
resolved at generation time would make two runs on different days produce
different output.

## Flags

| Flag | Effect |
|---|---|
| `--module-path <path>` | The Go module path written into `go.mod`. Defaults to the project name. |
| `--profile <name>` | The generation profile. `go-chi-postgres-svelte` is the only accepted value and the default. |
| `--module <name>` | Enable one compile-time module. Repeatable. Accepted: `notifications`, `organizations`, `totp`, `uploads`. |
| `--yes` | Accept every default and never prompt. |
| `--no-input` | Never prompt. Equivalent to `--yes` for scripts; both exist because a script's author means different things by them. |

Flags may appear before or after the project name. `nise new myapp --yes`
and `nise new --yes myapp` are the same command.

An unknown `--module` or `--profile` value fails immediately with the
accepted list, before any prompt and before any file is written.

## Prompts and non-interactive parity

Everything the prompts ask can be given as a flag, and everything the flags
set can be answered at a prompt. The two paths reach exactly the same
generator call.

Prompts appear **only** when all of these hold:

- output mode is human (so `--json` is always non-interactive — a prompt
  would corrupt the JSON document stream),
- stdout is a terminal,
- the `CI` environment is not detected,
- neither `--yes` nor `--no-input` was given.

Otherwise nothing is asked and nothing is read from standard input. A
non-interactive run with no project name is a usage error (exit `2`), never
a blocked read on a pipe that will never deliver.

The prompts, in order: the project name (if it was not given as an
argument), the Go module path (defaulting to the project name), and then one
`y/N` question per compile-time module. Every module defaults to **no**: an
unselected module contributes no import, no file, and no dead code, so
enabling one is always an explicit act.

## Refusing to destroy

The target directory must not exist, or must be empty. A directory holding
anything at all — including a dotfile a shell glob would miss, such as
`.git/` — is refused with an error naming what is in the way:

```
myapp is not empty: it contains .env.example, .gitignore, .nise/, AGENTS.md, and 4 more
Choose a different name, or remove that directory yourself first. nise new
has no --force: it never overwrites a directory it did not create.
```

Exit code `3` (precondition). **There is no `--force` flag.** The recovery
is always a different name or a removal the user performs themselves and can
therefore reason about.

Nothing is written before the check: the whole tree is rendered in memory
first, so a template failure or a non-empty target leaves the filesystem
exactly as it was.

## Name rules

The project name becomes a directory, a Go module path element, and the
`cmd/<name>/` package directory, so it has to satisfy all three at once. It
must:

- start with a lowercase ASCII letter, and contain only lowercase letters,
  digits, and single hyphens between them (`myapp`, `my-app`, `invoices2`);
- be at most 64 bytes;
- not be a Go reserved word (`func`, `range`, `type`, …);
- not be a reserved Windows device name, with or without an extension
  (`con`, `nul`, `aux`, `prn`, `com1`–`com9`, `lpt1`–`lpt9`, `con.txt`) —
  such a directory cannot be created on Windows at all, so a project that
  generated cleanly on Linux would be unclonable there;
- not end with a dot or a space, which Windows silently strips;
- contain no path separator, no `..`, no leading dot, no control character,
  no NUL byte, and no non-ASCII character.

Each rule fails with its own message and its own fix — an uppercase name is
told to use its lowercase form, a path is told to pass only the name. All of
them are usage errors (exit `2`), reported before any filesystem call, so a
rejected name creates nothing anywhere.

The `--module-path` value is validated separately: no empty, `.`, or `..`
path elements, no backslash, no control or non-ASCII character, and at most
255 bytes.

## The recipe it writes

`nise.json` at the project root, in the format
[ADR 0010](../adr/0010-project-recipe-format.md) fixes:

```json
{
  "schemaVersion": 1,
  "profile": "go-chi-postgres-svelte",
  "modules": [
    "organizations",
    "totp"
  ],
  "cliVersion": "v0.1.0",
  "runtimeVersion": "v0.1.0"
}
```

`modules` is sorted and deduplicated regardless of the order the flags were
given. Both version fields hold the **bare version string** of the `nise`
binary that generated the project — `v1.2.3`, a Go pseudo-version, or `dev`
for a source build — never a rendered banner, whose build timestamp the
determinism rule forbids in a generated file.

`nise new` also writes `AGENTS.md` and `.nise/architecture.json` through the
same code `nise agents` uses, so a project this command creates and one
`nise agents` regenerates cannot drift apart.

## Determinism

Two runs with the same inputs produce **byte-identical trees**, on any
machine.

- Every version the templates interpolate is a compiled-in literal, never
  resolved at generation time.
- No timestamp, hostname, username, absolute path, or random value reaches a
  generated byte. The reported root in `--json` output is the name the user
  typed, not an absolute path.
- Collections are sorted before they are emitted; nothing is written in map
  iteration order.
- File modes are fixed literals — `0644` for files, `0755` for directories —
  never inherited from the process umask. `os.WriteFile` and `os.MkdirAll`
  both mask their mode argument, so generation follows each with an explicit
  `os.Chmod`, which is not masked. Without that, the same project generated
  under `umask 077` would be `0600`/`0700` — identical in content and
  different on disk, which a recursive content diff cannot see.
  `TestFileModesAreIndependentOfUmask` generates under `umask 077` and
  requires exact equality.
- Every file is written with LF line endings and exactly one trailing
  newline, including on Windows and including from a checkout of this
  repository made with `core.autocrlf` enabled.

This is a tested guarantee, not an aspiration:
`internal/generator` generates twice into two temporary directories and
compares a recursive per-file SHA-256 manifest, and committed golden
manifests pin the exact path set and file contents for both the default
recipe and a recipe with every module selected.

## The tree it produces

```text
myapp/
├── .dockerignore                 [app]   keeps node_modules and stale builds out of the image
├── .env.example                  [app]   every variable, with safe placeholders
├── .gitignore                    [app]
├── .nise/architecture.json       [nise]  machine-readable layout map for coding tools
├── AGENTS.md                     [nise]  static instructions for external coding tools
├── Makefile                      [app]   build, run, test, fmt, lint, web-install, web-build
├── README.md                     [app]
├── go.mod                        [app]   pinned requires; no go.sum until `go mod tidy`
├── nise.json                     [nise]  the project recipe
│
├── api/README.md                 [app]   reserved for the OpenAPI document (M4)
├── cmd/myapp/
│   ├── main.go                   [app]   process and explicit database command dispatch
│   └── main_test.go              [app]   strict database command parsing
├── db/
│   ├── README.md                 [app]   migration conventions and safety boundary
│   ├── embed.gen.go              [nise]  embeds only readable migration SQL
│   ├── embed_test.go             [app]   source/embed byte parity
│   └── migrations/
│       └── 00001_baseline.sql    [app]   schema-neutral Goose baseline
│
├── deploy/
│   ├── Dockerfile                [app]   three-stage, non-root, distroless
│   ├── README.md                 [app]
│   └── compose.yaml              [app]
│
├── frontend/                     SvelteKit 2 / Svelte 5 on adapter-static
│   ├── .gitignore                [app]
│   ├── README.md                 [app]
│   ├── package.json              [app]   exact versions, packageManager pinned
│   ├── svelte.config.js          [app]   adapter output path into the Go package
│   ├── tsconfig.json             [app]
│   ├── vite.config.ts            [app]
│   ├── src/
│   │   ├── app.css               [app]   Tailwind entry; restores the a11y announcer
│   │   ├── app.d.ts              [app]
│   │   ├── app.html              [app]
│   │   └── routes/
│   │       ├── +layout.svelte    [app]
│   │       ├── +layout.ts        [app]   prerender = true, ssr = false
│   │       └── +page.svelte      [app]
│   └── static/favicon.svg        [app]
│
└── internal/
    ├── app/
    │   ├── app.go                [app]   the whole object graph, in one function
    │   ├── database.go           [app]   explicit status/migrate application surface
    │   ├── database_runtime.go   [app]   pool and transaction-runner construction
    │   ├── modes.go              [app]   mode resolution and the worker component
    │   └── modules.gen.go        [nise]  the compile-time module selection
    ├── features/README.md        [app]   reserved for vertical slices (M5, M7)
    └── platform/
        ├── config/
        │   ├── config.go         [app]   typed configuration over runtime/config
        │   └── config_test.go    [app]   pool default and validation contracts
        ├── database/
        │   ├── README.md         [app]
        │   ├── compatibility.go  [app]   read-only history and startup gate
        │   ├── compatibility_test.go [app] full history-state matrix
        │   ├── dbtest/            [app]   isolated real-PostgreSQL test databases
        │   │   ├── database.go    [app]   create, bound, redact, force-drop
        │   │   └── database_test.go [app] isolation and cleanup contract
        │   ├── migrate.go        [app]   instance-scoped, advisory-locked Goose provider
        │   ├── migrate_test.go   [app]   disposable PostgreSQL apply/rollback contract
        │   ├── migration_matrix_test.go [app] clean and supported-upgrade paths
        │   ├── pool.go           [app]   bounded pgxpool plus health/metric adapters
        │   ├── pool_test.go      [app]
        │   ├── sqlcsafety/        [app]   config/rule and output-confinement gate
        │   │   ├── config.go      [app]
        │   │   └── config_test.go [app]
        │   ├── testdata/migration-upgrades/
        │   │   └── 00001_baseline.sql [app] immutable release-schema fixture
        │   ├── transaction.go    [app]   pgx adapter for use-case-owned transactions
        │   └── transaction_test.go [app] sqlc DBTX propagation and option mapping
        ├── httpapi/router.go     [app]   the ordered middleware chain and the routes
        └── webui/
            ├── webui.go          [nise]  embeds the built frontend and serves the SPA
            └── embedded/placeholder.html  [nise]  makes `go build` work before `pnpm build`

```

58 files. Ownership markers are from
[Generated application layout](../generated-application-layout.md): `[nise]`
is regenerated and hand edits are lost, `[app]` is written once and never
overwritten. Every generated file declares the same thing in a header
comment in its own language's syntax. The four JSON files have no comment
syntax and declare ownership by path instead, which is the accommodation
[ADR 0009](../adr/0009-generated-application-layout.md) makes.

A directory the layout reserves for a later milestone is created holding a
`README.md` explaining what will live there and which seam is already
waiting for it. That is applied consistently: there is no mix of `.gitkeep`
files and omitted directories.

## What the generated project can already do

- **Typed configuration** from the environment, with `_FILE` secret
  indirection and fail-closed production validation. `APP_ENV=production`
  refuses to start with `DEBUG` enabled, a non-JSON log format, or a
  wildcard bind without `ALLOW_PUBLIC_BIND=true` — reporting every
  violation at once.
- **A conservative PostgreSQL pool** with explicit connection/lifetime
  bounds, startup connectivity proof, readiness integration, live pool
  metrics, and lifecycle closure. `nise dev` supplies its managed
  database URL automatically.
- **Readable embedded Goose migrations** with a schema-neutral version-one
  baseline, an instance-scoped provider, a dedicated connection, and a
  PostgreSQL advisory lock. `nise db status` inspects without mutation,
  `nise db migrate` applies explicitly, and startup blocks ahead, malformed,
  partial, or missing history without ever migrating.
- **Use-case-owned transactions** with closed isolation/access options,
  bounded rollback after cancellation or panic, explicit pgx adaptation, and
  compile-checked propagation into sqlc's `DBTX` shape. Nested runners fail
  instead of hiding a savepoint or independent commit.
- **Disposable PostgreSQL integration tests** with one random database per
  test, an eight-connection server limit, forced cleanup, protected connection
  material, local skip behavior, and CI-required configuration. The migration
  matrix proves clean installs and every committed supported release fixture;
  repository CI runs it against PostgreSQL 17.6.
- **Feature-local SQL safety checks** through pinned `sqlc vet`: unbounded
  `DELETE` and `UPDATE` plus every `TRUNCATE` fail, every config must keep
  generated Go inside its own `store/`, and the command is an explicit no-op
  before a feature exists.
- **Structured logging** in JSON or a readable text format, with a request
  ID and a correlation ID on every request and central redaction applied by
  the handler, not by call sites.
- **Health probes** at `/healthz/startup`, `/healthz/live`, and
  `/healthz/ready`, each answering with a status code and one of two fixed
  bodies and never naming a dependency.
- **Graceful shutdown**: SIGTERM fails readiness first, waits the drain
  period so a load balancer's next poll observes it, and only then stops
  accepting connections.
- **Process modes** `all`, `web`, and `worker`, selected by `-mode` or
  `APP_MODE`. `worker` never binds the HTTP port at all.
- **Security headers and a strict CSP** on every response, including
  responses produced by a panic or by a 404.
- **HTTP metrics** labeled by the router's resolved route template, exposed
  at `/internal/metrics` in Prometheus text format — mounted only when
  `OPERATOR_TOKEN` is set, and always behind it. Without the token the
  operator routes answer `404`, so an unauthorized caller cannot learn they
  exist.
- **An embedded SvelteKit page** served by the Go binary, hydrating under
  the strict CSP.

### The inline-script hash

The built SvelteKit fallback page contains exactly one inline `<script>`:
its hydration bootstrap. Its contents — and therefore its hash — change on
every build, and a per-response nonce cannot be baked into HTML written at
build time. The generated `internal/platform/webui` computes the SHA-256 of
each inline script from the embedded artifact **at startup** and the router
passes it to `secure.WithScriptHashes`. Without that the strict
`script-src 'self'` blocks the bootstrap and the page renders blank with a
console violation and no other symptom.

## What is deliberately absent

This command generates the foundation slice and nothing more. It does not
scaffold code for subsystems that do not exist yet.

| Absent | Arrives with |
|---|---|
| Generated feature `sqlc` output | M7 — the feature generator will write configs carrying the M3 query-safety contract; the validator and `sqlc-vet` target are present now |
| OpenAPI document, generated server bindings, typed frontend client | M4 — `api/` is the reserved seam, and `router.go` marks the `/api/v1` mount point |
| Sessions, authentication, authorization, audit log | M5 — `internal/features/` is the reserved seam |
| **The application shell**: sidebar, navigation, theme and language controls, authentication screens, tables, the component set, and the Vitest and Playwright suites | **M6** — `frontend/` is a minimal but real SvelteKit project today: it installs, builds, type-checks, and renders one page served by the Go binary. Nothing in the layout moves when M6 fills it in. |
| Feature and resource generation | M7 — see [`nise generate`](generate.md) |
| Background jobs and mail | M8 — `internal/app/modes.go` already wires a worker component whose loop registers nothing |

### `nise test` and the frontend suites

[`nise test`](test.md) looks for `test:unit` or `test:component`, and
`test:e2e` or `e2e`, in `frontend/package.json`. The generated
`package.json` declares **none of them**, so `nise test` reports the
component and end-to-end suites as `skipped` — which is accurate: no such
suite exists in this slice. The Vitest and Playwright integrations are M6's,
and adding a script that runs an empty suite would trade an honest `skipped`
for a meaningless `passed`. When M6 adds them, it must use the names
`nise test` already looks for.

### Compile-time modules today

`--module` records the selection in `nise.json` and writes it into
`internal/app/modules.gen.go`, which is the one regenerated file in
`internal/app/`. The modules' own runtime code lives under `modules/` in the
Nise repository and arrives with the milestones that build each one, so a
module selected today contributes its recorded selection and no behavior
yet. `modules.gen.go` is rewritable precisely because it holds the selection
and its constructor calls and nothing else.

## Exit codes

| Code | When |
|---|---|
| `0` | The project was generated. |
| `2` | A usage error: an invalid name or module path, an unknown profile or module, more than one positional argument, or no name on a non-interactive run. |
| `3` | A precondition failure: the target directory exists and is not empty, or exists and is not a directory. |
| `1` | Anything else, such as a filesystem permission failure. |

Every failure carries a machine-readable `code` in `--json` mode:
`new.invalid_name`, `new.invalid_profile`, `new.name_required`,
`new.target_not_empty`, `new.target_not_directory`.
