# Generated Frontend

This page describes planned V0.1 behavior.

Nise generates a complete authenticated operational application, not an empty Svelte starter and not a marketing template. Nise does not generate a marketing site; public content pages are a separate deployment.

## Build model

The authenticated application is built with the static adapter as a single-page application with a fallback page and embedded into the Go binary. Protected routes are never prerendered, and universal `load` functions run only in the browser. There is no per-request server-side rendering (see [ADR 0005](adr/0005-static-embedded-frontend.md)).

The root `+layout.ts` sets `ssr = false` and `prerender = false` for the whole tree, so `pnpm build` writes exactly one HTML document — the `fallback: 'index.html'` page — into `internal/platform/webui/embedded/client/`, and the Go server serves it for every path that is not a build asset. Prerendering is off rather than on because with `ssr = false` every route renders to the same empty shell: prerendering would write one identical copy per route and the fallback would then overwrite `index.html`. It also states the security rule structurally, since a protected route is then never written to disk at build time. A genuinely public, content-shaped page that needs no session may opt back in from its own `+page.ts`.

The server reads that document's single inline bootstrap script at startup and puts its hash into the Content Security Policy, so no build-time nonce or `'unsafe-inline'` is needed; a document carrying no inline script fails closed instead of being served under a policy that would blank it.

In a generated project `make dist` is the shipping build — `pnpm build` followed by the Go build that embeds its output. Plain `make build` deliberately does not run the frontend build, so a Go change can be compiled without Node; it embeds whatever build is present, or the committed placeholder page when there is none. `make web-clean` returns to the placeholder.

## Technology

- SvelteKit and Svelte 5 runes.
- Current event and snippet conventions; no generated legacy syntax.
- Tailwind CSS v4.
- Curated shadcn-svelte/Bits UI components copied into the application.
- Valibot for client-side form ergonomics; Go remains authoritative.
- TanStack Table behind application-owned Svelte components.
- Paraglide for internationalization.
- Vitest Browser Mode and selected Playwright flows.

## Svelte 5 conventions, and the check that holds them

Generated Svelte is runes-only, and so is the application that grows out of it. The conventions are one table, and each row's left column is refused by `pnpm check`:

| Svelte 4 | Use instead |
|---|---|
| `$: value = …` | `$derived` for a value, `$effect` for a side effect |
| `export let prop` | destructure from `$props()` |
| `$$props`, `$$restProps`, `$$slots` | take the rest of `$props()` explicitly; snippets replace slots |
| `createEventDispatcher` | a callback prop, such as `onselect` |
| `beforeUpdate` / `afterUpdate` | `$effect`, or `$effect.pre` for work that must precede the DOM update |
| `import { writable } from 'svelte/store'` | a `.svelte.ts` module holding `$state` |
| `on:click={…}` | `onclick={…}` |
| `<slot />`, `let:item` | a snippet prop and `{@render …}` |

Svelte 5 still accepts every construct in the left column, which is precisely why the check exists: none of them is a compile error or a `svelte-check` finding, so nothing else reports them. A codebase that is runes-only except for three files is one where a reader has to work out which mode a component is in before reading it, and where `$state` and a store subscription end up describing the same value.

The checker is `frontend/scripts/check-runes.mjs`, an application-owned Node script with no dependencies, run by `pnpm check` (and therefore by `make web-check`, `make all`, and `nise test`). It reads `<script>` blocks and markup separately, so a Tailwind `md:` class, a `$:` inside a string, and an `on:` inside a comment are not findings. It is application-owned like everything else under `frontend/`: an application may edit a rule, add one, or delete the file — but editing the rules is better than leaving a violation unreported, because the value is that the answer to "which mode is this component in" is the same for every file.

Nise's own test suite applies the same rules to every file `nise new` writes, so the starter can never be the source of a mixed-mode codebase.

## Application shell

The starter shell includes:

- Public and protected route groups.
- Collapsible desktop sidebar, top bar, breadcrumbs, account menu, and mobile drawer.
- API-client integration and consistent problem handling.
- Theme and language controls.
- Loading, empty, error, permission-denied, and not-found states.
- Accessible dialogs, confirmations, menus, sheets, tooltips, forms, tables, pagination, badges, alerts, toasts, and skeletons.

The optional compile-time modules (organizations, TOTP, notifications, uploads) add only the pages they require, such as organization switching and membership, TOTP setup and recovery codes, a notification centre, or upload management.

## Generated resources

A generated resource receives:

- URL-backed list search, filters, sorting, and pagination.
- Create and edit forms with typed models.
- Details view or panel.
- Permission-aware actions.
- Destructive-action confirmation.
- Responsive and keyboard-accessible behavior.
- Component tests and selected end-to-end coverage.

The result is editable application code, not a runtime CRUD renderer.

## Default visual system

- Professional, neutral, operational personality.
- Zinc/slate surfaces with cobalt-blue primary accent.
- Semantic success, warning, danger, and information tokens.
- System UI font stack with no external font request.
- Balanced density: comfortable forms and compact data tables.
- Medium radii, subtle borders, and restrained shadows.
- Light, dark, and system modes with manual override.
- Short functional motion with reduced-motion support.
- WCAG 2.2 AA target.

Avoid decorative gradients, glassmorphism, animated backgrounds, and oversized consumer-style controls.

## Branding

Normal application chrome carries the application's identity, not Nise branding. An application may retain a small “Built with Nise & Go” reference on an admin-only About/System page. It may remove that reference entirely.

