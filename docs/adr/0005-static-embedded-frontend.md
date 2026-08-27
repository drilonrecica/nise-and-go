# ADR 0005: Embed the Authenticated Frontend as a Static Single-Page Application

- **Status:** Accepted
- **Date:** 2026-08-27

## Context

SvelteKit can run with per-request server-side rendering (requiring a Node runtime or adapter) or as a statically built application. Nise targets one Go binary with no mandatory second runtime, and its applications are authenticated operational tools rather than content sites.

## Decision

The authenticated SvelteKit application is built with the static adapter as a single-page application with a fallback page and embedded into the Go binary. Protected routes are never prerendered; universal `load` functions run only in the browser against the generated API client.

Nise does not provide per-request SSR and does not generate a marketing site. Public marketing or SEO-oriented pages are a separate deployment.

## Consequences

- One deployment artifact; no Node runtime in production.
- No SEO or first-paint benefits of SSR for the authenticated application; acceptable for its audience.
- Dynamic routes rely on fallback routing, and the security-header/CSP policy must account for a client-rendered application.
- Revisit only if a real application requires SSR for authenticated pages.
