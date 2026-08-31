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

### How the shell is put together

Every route lives under `src/routes/(app)/`, whose layout is the shell, so a page never imports it and can never forget to. The pieces are in `src/lib/components/shell/`:

| File | What it is |
|---|---|
| `AppShell.svelte` | The composition: sidebar, drawer, top bar, skip link, and the `<main>` landmark. |
| `Sidebar.svelte` | The collapsible desktop rail, hidden below the medium breakpoint. |
| `NavDrawer.svelte` | The mobile drawer, a `<dialog>` opened with `showModal()`. |
| `SidebarNav.svelte` | The navigation list itself, drawn by both of the above. |
| `TopBar.svelte` | Menu button, breadcrumbs, theme control, account menu. |
| `Breadcrumbs.svelte` | The trail, derived from the URL. |
| `AccountMenu.svelte` | The account menu, over the shared `Menu` primitive. |

Navigation is **data**, in `src/lib/navigation.ts`: a list of sections, each with items carrying an href, a label, an icon, and optionally a required permission. The sidebar, the drawer, and the breadcrumb trail all read that one list, so adding a section is editing one file — which is also what lets `nise generate resource` append to it. Nothing outside the shell imports it, and a test enforces that.

A `permission` on an item hides it from a session that cannot use it. That is a courtesy and never a control: the server refuses the request regardless, and hiding a link the caller could still reach by typing its URL would be the only thing that changed.

Breadcrumbs label known paths from the navigation list and unknown segments from the segment itself, so a generated resource's detail page or a bare identifier still produces a readable trail rather than an empty one. A malformed percent escape in the URL is rendered as-is rather than throwing.

The current-section rule is a path-boundary match, not a string prefix: `/settings` claims `/settings/roles` and does not claim `/settings-archive`. An item marked `exact` — the dashboard at `/` — claims only itself, or it would be current on every page in the application.

### The component set

`src/lib/components/ui/` holds the curated set, as ordinary application-owned Svelte 5 components: `Alert`, `Badge`, `Button`, `Checkbox`, `ConfirmDialog`, `Dialog`, `Field`, `Icon`, `Input`, `Menu`, `Pagination`, `Select`, `Skeleton`, `Spinner`, `Table`, `Textarea`, `Toaster`, `Tooltip`, and the `toast` module. There is no `bits-ui` and no `shadcn-svelte`: the generated `package.json` has zero runtime dependencies. [ADR 0025](adr/0025-owned-ui-primitives.md) has the reasoning — in short, [ADR 0013](adr/0013-security-headers-and-csp.md)'s policy blocks the `style` attribute that every Floating UI integration writes to position a floating element, and weakening the policy to fit a dependency was not an option.

Three rules keep an owned set affordable:

- **Use the platform where the platform is better.** Modal surfaces are `<dialog>` with `showModal()`, which supplies the focus trap, the inert background, Escape, and the top layer. Selects are `<select>`, so type-ahead and the mobile picker come free. Checkboxes are real checkboxes coloured with `accent-color`, so forced-colors mode still draws a checkmark.
- **Position with CSS.** The floating elements are anchored to their trigger with ordinary absolute positioning. Collision detection is a reason to build one component for that case, not to take a dependency for all of them.
- **Write the keyboard model out in full where it has to be written.** `Menu` is the one component with a real one.

What each component insists on, and why:

| Component | The property it will not give up |
|---|---|
| `Button` | It is a `<button>` or an `<a>`, decided by whether the thing navigates, and the two prop sets are separate types. A link that acts and a button that navigates are the two most common defects here, and both are invisible until somebody middle-clicks or presses space. While loading, the label keeps its width and the busy state is announced, not just drawn. |
| `Field` | It wires the label, help text, error, and `aria-describedby`/`aria-invalid` to the control by id. Doing that per form is how a form ends up with an error that is visible and unannounced. The required marker is the word, not a lone asterisk. |
| `Dialog` / `ConfirmDialog` / drawer | `showModal()`, always. A hand-rolled modal eventually gets the focus trap wrong, and a wrong focus trap is worse than none. `ConfirmDialog`'s typed-confirmation option is documented as being for the genuinely irreversible; asking routinely trains people to type past it. |
| `Menu` | Open on click or Arrow, arrow navigation with wrapping, Home/End, Escape and Tab to dismiss, focus returned to the trigger. Escape is handled on the document, because a mouse-opened menu leaves focus on the sibling trigger. |
| `Tooltip` | `aria-describedby`, never `aria-labelledby`: a tooltip supplements a control that is already named. Hover, focus, and Escape to dismiss are all WCAG 2.2 SC 1.4.13, and Escape is the one people forget. |
| `Toaster` | One live region, mounted in the shell from first paint. A live region created when the first message arrives is one the screen reader was not watching, so that message is announced to nobody. Polite, not assertive: anything that must interrupt is not a toast. |
| `Badge` / `Alert` | The tone reinforces; the word carries the meaning (SC 1.4.1). `Alert` announces only when it appears in response to something the person did. |
| `Skeleton` | `aria-hidden`. It decorates a region that already says it is busy; announcing each grey rectangle is how a loading table becomes forty announcements. |
| `Table` | The caption is required — screen readers list tables by caption — and the scroll container is focusable, or a wide table cannot be read without a mouse. |
| `Pagination` | Both controls are real links with real hrefs, because paging is navigation: it belongs in history and must survive being opened in a new tab. An unavailable page is a disabled control that stays put rather than one that vanishes and moves its neighbour under the cursor. |

### The accessibility properties that are easy to lose

Each of these is invisible when it is gone, so each is pinned by a test:

- **A skip link** first in the tab order, visible only on focus. Without it a keyboard user passes every navigation link on every page before reaching the content.
- **Named landmarks.** Two `<nav>` elements in one document must each carry a label, or a screen reader offers a list of identical "navigation" entries.
- **`aria-current="page"`** on the current navigation item and the last breadcrumb, which is a `<span>` rather than a link — a link to the page you are on is a link that does nothing.
- **A collapsed sidebar keeps its labels**, hidden visually with `sr-only` rather than `display: none`, which would leave a screen reader with an unlabelled link and an icon it cannot describe.
- **The drawer is a modal `<dialog>`.** `showModal()` supplies the focus trap, the inert background, Escape-to-close, and the top layer. Every one of those is something a hand-written drawer gets wrong, and a broken focus trap is actively hostile.
- **The menu implements the full keyboard model** — open on click or Arrow, arrow navigation with wrapping, Home/End, Escape and Tab to dismiss — and returns focus to its trigger on dismissal. Escape is handled on the document rather than on the menu, because opening with the mouse leaves focus on the trigger, which is a sibling of the menu; a handler on the menu would never see it.

Icons are path data in `src/lib/components/ui/icons.ts`, drawn by one small component. There is no icon package: an icon is one or more `d` attributes on a 24×24 grid with a round stroke that takes `currentColor`, so the set stays as small as the application needs, and a single set reads correctly on both themes.

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

### The token system

Every color, radius, shadow, duration, and control height a component uses is a design token declared in `frontend/src/app.css` and nowhere else. A component writes `bg-surface text-text-muted border-line`, never `bg-white text-zinc-600 border-zinc-200`, so rebranding is editing two blocks rather than every component that ever drew a border. Nise's own test suite refuses a raw palette utility (`bg-zinc-100`, `text-blue-600`) anywhere in generated Svelte, because a color spelled that way is one a rebrand cannot reach.

Tokens are named by role, not by shade. The distinction that matters most is between `--line`, the decorative rule between things that are already distinguishable by their surfaces, and `--control-line`, the boundary that tells a person where an input is: WCAG 2.2 SC 1.4.11 applies to the second and not the first, which is why only the second is held to 3:1.

The roles are surfaces (`canvas`, `surface`, `surface-muted`, `surface-raised`, `overlay`), text (`text`, `text-muted`, `text-subtle`, `text-inverted`), lines (`line`, `line-strong`, `control-line`), the single accent (`primary`, `primary-hover`, `primary-active`, `primary-text`, `primary-soft`, `primary-on-soft`), four semantic states each with a solid fill, a text color, a soft background, and a line (`success`, `warning`, `danger`, `info`), and `ring`.

`@theme inline` publishes each role to Tailwind by reference rather than by value. The `inline` is load-bearing: without it Tailwind copies the current value into its own variable at build time and both themes render as light.

`:focus-visible` is styled once, globally, on the ring token — as an outline rather than a box-shadow, so forced-colors mode keeps it — so no component can ship without a focus indicator.

### Light, dark, system, and reduced motion

Three preferences, two themes. A person chooses `light`, `dark`, or `system`; what renders is always `light` or `dark`, written to `<html data-theme>`. The preference is stored, the resolution never is — storing the resolution would freeze a `system` reader into whichever mode their machine was in the day they last visited.

`src/lib/theme.svelte.ts` owns it: a `.svelte.ts` module holding `$state`, started from an `$effect` in the root layout, which adopts the stored preference and then follows `prefers-color-scheme` for as long as the page is open. That listener is what makes `system` live rather than a value sampled once at load.

A small inline script in `app.html` resolves the theme before first paint, because a module cannot run before the browser paints the page background and a dark-mode reader would otherwise see a white flash on every cold load. It repeats exactly two facts from the module — the storage key and the three preference names — and a Nise test asserts the two still agree, because a disagreement is a reader whose choice is silently discarded with nothing failing anywhere. Its hash reaches the Content Security Policy automatically: `internal/platform/webui` reads every inline script out of the built document at startup.

Storage is treated as optional throughout. Private browsing modes make `localStorage` throw, and a theme preference is not worth a blank page, so both reads and writes swallow and fall back to `system`.

Reduced motion is enforced once, globally, in `app.css` rather than per component — a per-component decision is one somebody forgets, and what they forget is what makes a reader ill. Every transition and animation collapses to a single frame rather than being removed, so a `transitionend` listener still fires and a component waiting for one to unmount an element does not hang. Motion that CSS cannot reach — a scripted scroll, a count-up — reads `preferences.reducedMotion`, which watches the same query.

### Contrast is checked, not intended

`pnpm check` runs `scripts/check-contrast.mjs`, a dependency-free script that reads `app.css`, extracts both theme blocks, and proves 27 foreground/background pairs meet WCAG 2.2 AA in each — 54 assertions in total. Text pairs must reach 4.5:1 (SC 1.4.3) and control boundaries and the focus ring 3:1 (SC 1.4.11).

It exists because contrast is the one accessibility property a palette either has or does not have, that no amount of careful component code recovers, and that a rebrand breaks silently: somebody swaps in their own brand blue, every button still looks fine to them, and the application is unreadable for a share of its users. The failure message names the pair, its measured ratio, and the ratio it needs, because fixing tokens until it passes is the intended rebranding workflow.

Nise's default palette is held to the same table by a Go test over the generated bytes, since this repository's own CI runs Go and no Node.

## Branding

Normal application chrome carries the application's identity, not Nise branding. An application may retain a small “Built with Nise & Go” reference on an admin-only About/System page. It may remove that reference entirely.

