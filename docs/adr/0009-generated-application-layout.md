# ADR 0009: Generated Application Directory Layout

- **Status:** Accepted
- **Date:** 2026-08-27

## Context

`nise new` writes a whole repository. Every later decision inherits that tree: `nise generate` places files in it, `nise check` asserts invariants over it, `nise upgrade` migrates it, and the OCI image copies from it. Applications built on `0.1` keep this shape for years, so moving a directory later is a migration for every project in the field, not a refactor.

Four constraints pull against each other:

- One binary embeds the built SvelteKit application, the migrations, and the mail templates, so a deployment is a single artifact with no sidecar asset server and no separately versioned migration bundle.
- `go:embed` cannot reach outside the directory of the package that declares it, so the embedding Go package must be an ancestor of the build output.
- The frontend must stay a normal pnpm project that a frontend developer recognizes, editable with the usual tools and without learning a Nise-specific layout.
- Ownership must be legible from the path: a reader must be able to tell whether a directory is regenerated or hand-owned without consulting a manifest ([ADR 0003](0003-application-ownership.md)).

## Decision

The generated tree is documented entry by entry in [Generated application layout](../generated-application-layout.md). This record fixes the load-bearing choices.

### Top-level shape

Three trees hold artifacts a non-Go contributor edits, and they stay at the top level: `api/` for the OpenAPI document, `db/` for readable SQL migrations, and `frontend/` for the SvelteKit project. Go code lives under `cmd/<app>/` and `internal/`, and `internal/` has exactly three children:

- `internal/app/` — explicit constructor wiring and process-mode selection.
- `internal/features/<feature>/` — one vertical slice per feature, owning its handlers, use cases, SQL, domain rules, and tests.
- `internal/platform/` — technical infrastructure the application owns: configuration, database, HTTP transport, jobs, mail, and the embedded frontend.

`internal/platform/` is for technical infrastructure only. A domain rule belongs in a feature. `internal/util`, `internal/common`, `internal/shared`, and `internal/pkg` are excluded by name.

### The SvelteKit tree and the embed point

`frontend/` is a pnpm project containing no Go files. `internal/platform/webui/` is a Go package containing no frontend sources. They are reconciled by pointing the static adapter's output at the Go package:

```js
// frontend/svelte.config.js
adapter({
  pages: '../internal/platform/webui/embedded/client',
  assets: '../internal/platform/webui/embedded/client',
  fallback: 'index.html',
})
```

```go
// internal/platform/webui/webui.go
//go:embed all:embedded
var embedded embed.FS
```

Three details are load-bearing:

- The `all:` prefix is mandatory. SvelteKit writes its hashed bundles into `_app/`, and without `all:` `go:embed` skips every path whose name begins with `_` or `.`.
- The embed pattern names `embedded/`, not `embedded/client/`. The adapter deletes its output directory before every build, so anything committed inside `embedded/client/` would be destroyed by `pnpm build`. The committed placeholder is its sibling.
- `internal/platform/webui/embedded/placeholder.html` is committed. It is the only reason `go build ./...` succeeds in a fresh checkout where `pnpm build` has never run: the pattern `all:embedded` matches at least one file. A missing `embedded/client/index.html` is a startup error in production (fail closed) and serves the placeholder page in development.

### Core services and optional modules

Every generated application contains the same core services — sessions and authentication, authorization, the audit log, jobs, and mail. They get no privileged layer of their own. One rule places them, and it is the same rule that places anything else in the tree:

> A core service is a **feature** when it owns domain rules, its own tables, and its own endpoints. It is **platform** when it is a technical adapter with no domain rules of its own.

That puts sessions and authentication in `internal/features/auth/`, authorization in `internal/features/authz/`, and the audit log in `internal/features/audit/`; it puts the River runner in `internal/platform/jobs/` and the mail interface, SMTP transport, and templates in `internal/platform/mail/`. A job's payload and a message's content belong to the feature that enqueues or sends them, not to the runner or the transport.

Nothing structurally distinguishes a core feature from a generated one. `internal/features/auth/` has the shape of `internal/features/invoice/`, is application-owned in the same way, and may be edited, rewritten, or deleted. The only difference is when it is written: `nise new` writes the core features and `nise generate` writes the rest. `nise check` does not require any feature directory to exist, which keeps it an invariant runner rather than a starter-conformity checker.

Because core features are peers rather than a base layer, a feature depends on another feature only through an interface the consumer declares, and never on another feature's `store` package. Authorization checks, audit records, and job enqueueing all reach a feature that way.

The four compile-time modules — `organizations`, `totp`, `notifications`, `uploads` — are recorded in the recipe ([ADR 0010](0010-project-recipe-format.md)) and selected at generation time. A selected module contributes its runtime code as an import of `github.com/drilonrecica/nise-and-go/modules/<name>`, explicit constructor wiring in `internal/app/modules.gen.go`, an application-owned `internal/features/<name>/` where it has a transport or domain surface, its tables in `db/migrations/`, and its screens under `frontend/src/routes/(app)/<name>/`. An unselected module contributes nothing at all: no import, no file, no dead code behind a runtime flag.

`internal/app/modules.gen.go` is the one regenerated file in `internal/app/`. It holds constructor calls and nothing else, so that changing the module selection can rewrite it without destroying application edits — generation refusing to destroy, by giving itself nothing to destroy. There is no registry, no `init()` registration, and no reflection-driven discovery.

### Generated code and its owners

| Artifact | Path | Generator | Owner |
|---|---|---|---|
| OpenAPI document | `api/openapi.yaml` | hand-written | application |
| Strict chi server bindings | `internal/platform/httpapi/openapigen/` | oapi-codegen | Nise, regenerated |
| Typed SQL access | `internal/features/<feature>/store/` | sqlc | Nise, regenerated |
| SQL queries | `internal/features/<feature>/queries/` | hand-written | application |
| TypeScript models | `frontend/src/lib/api/schema.d.ts` | openapi-typescript | Nise, regenerated |
| Typed fetch client | `frontend/src/lib/api/client.ts` | written once | application |
| Built SPA | `internal/platform/webui/embedded/client/` | `pnpm build` | Nise, regenerated, not committed |

Ownership is readable from the path: the directory names `store/`, `openapigen/`, and `embedded/`, and any file ending `.gen.go`, mark regenerated output. Everything else is application-owned.

Every generated file carries one of two exact headers, in the comment syntax of its language:

```text
Code generated by nise; DO NOT EDIT.
Generated once by nise; owned by this application. Nise will not overwrite it.
```

The first form matches Go's generated-code convention so linters and diff tools skip it. Neither header records a version: a version in the header would rewrite every file on every CLI bump and break the clean-regeneration check. JSON files, which have no comment syntax, declare ownership by path instead; `nise.json` is the only one.

### Naming

- Feature directories are singular lowercase nouns and equal their Go package name: `internal/features/invoice`. The REST collection (`/api/v1/invoices`), the table (`invoices`), and the route group (`frontend/src/routes/(app)/invoices/`) are plural.
- A feature's `store` package is imported with the alias `<feature>store` where more than one is in scope.
- Migrations use zero-padded sequential prefixes, `NNNNN_snake_case.sql`. Timestamped names are excluded because `nise new` must produce byte-identical output on every machine ([ADR 0002](0002-deterministic-generation.md)).
- Go directory names carry no underscores or hyphens. Svelte components are PascalCase; SvelteKit route files keep their `+page.svelte` conventions.
- No two paths may differ only by case, because Windows and default macOS filesystems cannot represent both.

### Explicitly excluded

- Placing the SvelteKit project inside a Go package directory, or putting `embed.go` in the pnpm project root. It compiles, but it hands a frontend developer a Go file in the directory they consider theirs.
- Committing a placeholder inside the adapter's own output directory. `pnpm build` deletes it, so every build would dirty the working tree.
- A top-level `web/` directory. It collides with the `web` process mode in conversation and in documentation.
- A generic `internal/pkg/`, `internal/util/`, or `internal/common/`.
- A separate `internal/core/` or `internal/services/` for the core services. It would add a fourth child of `internal/` and force a boundary judgement — core or feature? — on everything written afterwards.
- A second binary. Migrations and workers are modes and subcommands of the one application binary.

## Consequences

- One `go build ./...` produces a runnable binary from a fresh clone, before Node is installed. What it serves is the placeholder, not an application.
- `pnpm build` writes outside `frontend/`. This is surprising once, is stated in `frontend/README.md` and in `svelte.config.js`, and is the price of keeping both trees single-language.
- Both the Go and the pnpm tree sit inside the module, so `./...` and `gofmt -l .` walk `frontend/node_modules/` — measured, not assumed. An npm package that ships a `.go` file is listed as a package of the application. Generated tooling therefore names its Go targets explicitly, `./cmd/... ./internal/... ./db/...`, rather than relying on `./...`.
- Feature-colocated sqlc output means one `sql:` block per feature in `sqlc.yaml`, which becomes Nise-owned and regenerated as features are added.
- Core services sit beside custom features, so `internal/features/` is not a list of things the application invented. The compensation is the interface rule: a feature that needs authorization, auditing, or a job declares the interface it wants where it uses it, which also keeps those dependencies visible in the consumer rather than in a shared base package.
- Nise's own repository ignores `dist/`, `build/`, `node_modules/`, and `.svelte-kit/`. Template files for this layout must avoid those path segments or they will not be committed — the reason the embed directory is named `embedded/client/` and not `dist/`.
- Revisit if SvelteKit's static adapter stops supporting an output path outside the project root, or if a real application needs a second deployable binary.
