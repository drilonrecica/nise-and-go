# ADR 0013: Security Headers and Content Security Policy

- **Status:** Accepted
- **Date:** 2026-08-27

## Context

Nise generates applications whose authenticated frontend is a SvelteKit single-page application built with `adapter-static`, embedded in the Go binary with `go:embed`, and served by the Go server from the same origin as the API ([ADR 0005](0005-static-embedded-frontend.md), [Generated application layout](../generated-application-layout.md)). Whatever policy that server sends is inherited by every application Nise produces, and a Content Security Policy that is wrong in the permissive direction is a silent weakness: nothing fails, nothing is logged, and the mistake survives until somebody exploits it.

The working threat model has carried one open question since the frontend stack was chosen: whether a CSP for the embedded Svelte application can avoid unsafe inline styles and scripts with that stack. It is a real question rather than a rhetorical one. The received answer in the SvelteKit ecosystem is that it cannot, on two grounds — SvelteKit emits an inline bootstrap script, and SvelteKit's own configuration documentation states that "if using Svelte transitions, developers must either omit the `style-src` directive or include `unsafe-inline`". The default visual system calls for transitions on dialogs, drawers, and menus ([Generated frontend](../frontend.md)), so on that reading every generated application would ship `style-src 'unsafe-inline'`.

The milestone-6 application shell does not exist yet, so the question could not be settled by building the real thing. It could, however, be settled better than by assumption.

## Decision

### What was measured

A representative application was scaffolded outside the repository and built: SvelteKit 2.70.3, `@sveltejs/adapter-static` 3.0.10 (SPA with `fallback: 'index.html'`), Svelte 5.56.10, Tailwind CSS 4.3.3, Vite. It was served by a small Go static server that sent the candidate policy as a real response header, and driven in Chrome with a `securitypolicyviolation` listener attached. The findings:

1. **The fallback page contains exactly one inline `<script>`** — SvelteKit's hydration bootstrap, which sets a global and dynamically imports `start.js` and `app.js`.
2. **Its content, and therefore its SHA-256, changes on every build.** The global it defines is named `__sveltekit_<random>`, and the module URLs it imports are content-hashed. Two consecutive builds of an unchanged source tree produced different hashes. A hash written into Go source would be wrong after the next `pnpm build`.
3. **SvelteKit's own `kit.csp` cannot own the policy for this stack.** For prerendered output it emits the policy as a `<meta http-equiv="content-security-policy">` tag, and `frame-ancestors` and `report-uri` are ignored in a meta tag. It also selects hashes rather than nonces for prerendered pages, on the correct ground that a nonce baked into a static file is not a nonce.
4. **Tailwind v4 output is an external stylesheet.** The built page links `_app/immutable/assets/*.css`; there is no inline `<style>` element. SvelteKit's `inlineStyleThreshold` defaults to 0, which is what keeps it that way.
5. **The bundle contains no `eval` and no `Function` constructor**, and no `insertRule`, `createElement('style')`, or `cssText`.
6. **Svelte 5 transitions raise no `style-src` violation.** `transition:fade`, `transition:fly`, and `transition:slide` all animated correctly under `style-src 'self'`. The bundle drives them through `element.animate()` — the Web Animations API — which CSP does not govern. The SvelteKit documentation note quoted above describes Svelte 4's stylesheet-injection implementation and is stale for Svelte 5.
7. **The `style:` directive raises no violation** either; it compiles to CSSOM property writes, which CSP does not govern.
8. **Svelte's component-level custom-property syntax adds reports, but not breakage.** `<Component --token="value" />` compiles to a `<svelte-css-wrapper>` element carrying `style="display: contents; --token: value"`, which is reported under `style-src-attr`. Measured: the theming nevertheless works — the token resolves and the wrapper still computes to `display: contents` — because Svelte applies both through CSSOM after creating the element, and CSP does not govern CSSOM writes. The cost is two reports per component instance, scaling linearly (three components produced six reports, plus the announcer's two). This matters because CSS custom properties are the theming primitive for generated applications, so it is precisely the syntax an M6 author reaches for. The correct response is a stylesheet rule or a class, not `AllowInlineStyleAttributes`: nothing is broken, so a permanent weakening to quiet the reports would be a bad trade.
9. **Two `style-src-attr` violations remain, from one source.** Both come from SvelteKit's own accessibility announcer, a `<div id="svelte-announcer">` its client runtime creates with a hard-coded inline `style` attribute holding the standard visually-hidden declarations. It is framework code; an application cannot remove it. A literal `style="display: contents"` attribute in SvelteKit's conventional `app.html` produced the same violation and *was* removable, by moving the declaration to a class.

### The answer to the open question

**Yes.** A generated application can run under a policy with no `'unsafe-eval'`, no `'unsafe-inline'` in `script-src`, and no `'unsafe-inline'` in `style-src` or `style-src-elem`.

The residual is confined to `style-src-attr` and comes from framework-generated markup in two places, neither of which requires weakening the policy:

- **SvelteKit's accessibility announcer** — two reports per page load, and the only one with a functional consequence. An `#svelte-announcer` rule in `app.css` restores the visually-hidden geometry from the external stylesheet, verified in the browser, so nothing is visibly or functionally wrong while the blocked attribute is simply never applied.
- **`<svelte-css-wrapper>`, from the component custom-property syntax** — two reports per component instance, and no functional consequence at all: the styles are applied through CSSOM, which CSP does not govern, so the theming works. Avoidable by scoping the token from a stylesheet rule.

`style-src-attr 'unsafe-inline'` is therefore **not** in the default policy. It is available as `AllowInlineStyleAttributes(reason)`, which records a waiver.

### The policy

`runtime/secure` ([ADR 0011](0011-runtime-public-api.md) assigns the package) builds two policies. The production document policy is:

```
Content-Security-Policy: default-src 'none'; base-uri 'none'; connect-src 'self';
  font-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:;
  object-src 'none'; script-src 'self'; style-src 'self'
Cross-Origin-Opener-Policy: same-origin
Cross-Origin-Resource-Policy: same-origin
Permissions-Policy: <every unused capability denied; fullscreen=(self)>
Referrer-Policy: same-origin
Strict-Transport-Security: max-age=31536000; includeSubDomains
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
```

A development policy is identical minus `Strict-Transport-Security`. The per-directive rationale is in [Security headers](../security-headers.md).

The API policy is strictly stricter: `default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; sandbox`. It names no `script-src`, `style-src`, `img-src`, or `connect-src` at all, so everything falls through to `default-src 'none'`, and `sandbox` makes an API response that a browser is talked into rendering as a document originless and inert. `sandbox` does not affect `fetch` or `XMLHttpRequest`, which create no document.

### Inline script: hash for the shell, nonce for what Go renders

Because the shell's HTML is written at build time, the Go server computes the SHA-256 of each inline script in the embedded `index.html` **at startup, from the embedded artifact**, and passes it to `WithScriptHashes`. That is the only correct place: finding 2 rules out a constant in Go source, and finding 3 rules out delegating to SvelteKit's meta tag.

Per-response nonces exist alongside it, minted from `crypto/rand` and exposed through the request context, for HTML the Go server renders itself — the pre-build placeholder page, error documents, anything server-rendered later. Minting is lazy: the nonce is created only when a handler calls `Nonce(ctx)`, and only such a response is marked `Cache-Control: no-store`. Eager minting would either have pushed every immutable build asset out of shared caches or published a nonce nothing used.

`kit.csp` is left unconfigured in generated `svelte.config.js`, so the Go response header is the single authoritative policy. Two policies both apply as their intersection, and an application debugging a blocked resource should not have to discover a second one hiding in a meta tag.

### The contentious ones, decided

- **HSTS `preload`: no, opt-in.** Preloading is an approximately irreversible act that covers the registrable domain and every present and future subdomain, and it belongs to the domain's operator, not to the framework that generated the application. The token is also meaningless until the operator submits at hstspreload.org, so emitting it by default asserts a submission nobody made. `WithHSTSPreload()` turns it on and enforces the list's own requirements.
- **COEP: not sent.** `require-corp` is a capability enabler for `SharedArrayBuffer` and high-resolution timers, which an operational CRUD application does not use. What it does immediately is break every cross-origin subresource that has not opted in with CORP or CORS — in a Nise application, the uploads module's object-storage attachments behind presigned URLs. Paying a visible breakage for a capability nothing uses is a bad trade. `WithCrossOriginEmbedderPolicy` is there for an application that has a use.
- **`X-Frame-Options`: yes, but derived.** It is redundant with `frame-ancestors 'none'` on every browser that implements CSP Level 2, and it earns its 22 bytes only in middleboxes and embedded webviews that strip or fail to parse a CSP header while still honoring it. The real risk is the two disagreeing, which is why one option drives both: relaxing `frame-ancestors` removes `X-Frame-Options`, because `ALLOW-FROM` was never broadly implemented and leaving `DENY` behind would make the application load in one browser and refuse in another with the CSP looking correct in both.
- **`Referrer-Policy: same-origin`**, not `no-referrer`. Identical privacy — neither sends anything cross-origin — and `same-origin` additionally keeps `Referer` available for the application's own requests, where a request-forgery check may use it as a fallback when `Origin` is absent.
- **`X-XSS-Protection`: not sent.** The legacy auditors it controlled are gone from every current browser, and the one value still sometimes recommended (`0`) protects only against auditor bugs in browsers this project does not support.

### Configuration cannot silently remove a protection

`Policy` has no exported fields and no setters, because a settable field is a protection an application can remove with a line that reads like configuration. Every adjustment is an option, and the option names carry the property: `With…` never weakens anything; `Allow…` weakens something, takes the reason as its first argument, refuses an empty reason, and records a `Waiver` readable from `Policy.Waivers()`. There is no option that deletes a header, and no option can produce `'unsafe-eval'`, `'unsafe-inline'` in `script-src`, `'unsafe-hashes'`, `'wasm-unsafe-eval'`, or `*` — those are refused by the shared source validator, so the weakening does not exist in the API rather than being discouraged in prose.

The middleware writes the policy's headers at response-commit time and overwrites what is already there, so a handler further down the chain cannot delete or downgrade one either.

## Consequences

- The threat model's security-header question is closed with measurement rather than assumption, and the residual is named, bounded, and cheap.
- The M6 shell adds what the probe did not have: shadcn-svelte/Bits UI (and Floating UI's positioning, which writes through CSSOM and is expected to be invisible to CSP but was not observed), TanStack Table, and Paraglide. **This must be re-verified when that shell exists**, by the browser test described in [Security headers](../security-headers.md#confirming-the-policy-when-the-application-shell-exists): drive the shell under the enforced production policy with a `securitypolicyviolation` listener and require that no violation names a directive other than `style-src-attr`, that every `style-src-attr` violation is attributable to framework-generated markup rather than to a file Nise wrote, and — asserted on computed style, not on the absence of violations — that theme tokens actually apply. Until that test exists and passes, the claim in this ADR is evidence about a representative build, not about the shipped shell.
- The generated `frontend/src/app.css` must carry the `#svelte-announcer` rule, and the generated `app.html` must not use a literal `style` attribute. Both are `[app]`-owned files Nise writes once, so this is a generation requirement, not something the runtime can enforce. A `nise check` rule that fails on a literal `style="` in generated Svelte sources would close the gap and is not built here.
- The generated `internal/platform/webui` handler must compute the bootstrap hash from the embedded artifact at startup. It does not exist yet; `WithScriptHashes` is the seam it will use.
- The application's `svelte.config.js` must leave `kit.csp` unset and `kit.inlineStyleThreshold` at its default. Raising the threshold inlines stylesheets and would require hashes in `style-src`.
- Two CSP violation reports per page load is real noise. It will appear in browser consoles and in any report collector an application configures, and operators must not learn to ignore reports because of it. If SvelteKit stops setting that attribute, or offers a way to opt out, the residual disappears and nothing else changes.
- Revisit this decision if the shell's component libraries turn out to need inline style attributes after all — in which case `AllowInlineStyleAttributes` becomes a default rather than an escape hatch, and that change is itself a recorded waiver — or if a Nise application ever needs per-request server-side rendering, which would make nonces the natural default over hashes.
