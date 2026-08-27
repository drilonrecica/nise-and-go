# Generated Frontend

This page describes planned V0.1 behavior.

Nise generates a complete authenticated operational application, not an empty Svelte starter and not a marketing template. Nise does not generate a marketing site; public content pages are a separate deployment.

## Build model

The authenticated application is built with the static adapter as a single-page application with a fallback page and embedded into the Go binary. Protected routes are never prerendered, and universal `load` functions run only in the browser. There is no per-request server-side rendering (see [ADR 0005](adr/0005-static-embedded-frontend.md)).

## Technology

- SvelteKit and Svelte 5 runes.
- Current event and snippet conventions; no generated legacy syntax.
- Tailwind CSS v4.
- Curated shadcn-svelte/Bits UI components copied into the application.
- Valibot for client-side form ergonomics; Go remains authoritative.
- TanStack Table behind application-owned Svelte components.
- Paraglide for internationalization.
- Vitest Browser Mode and selected Playwright flows.

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

