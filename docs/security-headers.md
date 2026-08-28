# Security Headers and Content Security Policy

This page is for the author of an application Nise generated. It describes the response headers `runtime/secure` sends, why each one is there, and how to change one without quietly losing a protection.

The decision, the evidence behind it, and what still has to be re-verified are in [ADR 0013](adr/0013-security-headers-and-csp.md).

This page describes planned V0.1 behavior. `runtime/secure` is implemented; the generated wiring that mounts it arrives with the milestone it belongs to.

## What gets sent

Two policies, mounted on two subtrees.

### Document policy — production

```http
Content-Security-Policy: default-src 'none'; base-uri 'none'; connect-src 'self'; font-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self'
Cross-Origin-Opener-Policy: same-origin
Cross-Origin-Resource-Policy: same-origin
Permissions-Policy: accelerometer=(), ambient-light-sensor=(), autoplay=(), battery=(), browsing-topics=(), camera=(), display-capture=(), encrypted-media=(), fullscreen=(self), geolocation=(), gyroscope=(), idle-detection=(), local-fonts=(), magnetometer=(), microphone=(), midi=(), payment=(), publickey-credentials-get=(), screen-wake-lock=(), serial=(), usb=(), xr-spatial-tracking=()
Referrer-Policy: same-origin
Strict-Transport-Security: max-age=31536000; includeSubDomains
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
```

### Document policy — development

Byte-identical, minus `Strict-Transport-Security`.

Development does not get a looser CSP. There is no `'unsafe-eval'` for a dev build and no relaxed `connect-src`, because a policy that only holds in production is a policy nobody exercises until it breaks in production.

### API policy

```http
Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; sandbox
```

plus the same non-CSP headers as the document policy.

## Why each directive is there

| Directive | Value | Why |
|---|---|---|
| `default-src` | `'none'` | The base. Nothing loads from anywhere until a directive below says otherwise, so a resource type nobody thought about is denied rather than inherited. |
| `base-uri` | `'none'` | Stops an injected `<base>` from repointing every relative URL on the page at an attacker's origin. It does not fall back to `default-src`, so omitting it would leave it unrestricted. |
| `connect-src` | `'self'` | `fetch` and `XMLHttpRequest` reach the application's own API and nothing else. This is the directive that decides where injected script can send what it stole, so it is the one to widen last. |
| `font-src` | `'self'` | The generated frontend uses a system font stack precisely so no font is fetched from a third party. `'self'` still permits a self-hosted face. |
| `form-action` | `'self'` | Stops an injected form from posting credentials off-origin. Does not fall back to `default-src`. |
| `frame-ancestors` | `'none'` | Clickjacking. Does not fall back to `default-src`. |
| `img-src` | `'self' data:` | `data:` is needed for images the application generates itself — the TOTP enrollment QR code is the concrete case. A `data:` URL in an `<img>` cannot execute script. |
| `object-src` | `'none'` | `<object>` and `<embed>` are plugin surface with no use in this kind of application. Redundant with `default-src` today, explicit so it survives a change to it. |
| `script-src` | `'self'` | Same-origin bundles only. Inline script is allowed by hash or nonce, never by keyword. |
| `style-src` | `'self'` | Tailwind v4 output is an external stylesheet, so this is sufficient. See [The one residual](#the-one-residual). |

Directives deliberately absent: `frame-src`, `media-src`, `child-src`, and `manifest-src` all fall back to `default-src 'none'`, so naming them would add bytes and no restriction. `worker-src` falls back through `child-src` to `script-src`, so a worker is limited to `'self'`, which is right. `upgrade-insecure-requests` is absent because every subresource is already same-origin and HSTS covers the transport.

## Why each header is there

**`Strict-Transport-Security: max-age=31536000; includeSubDomains`**, production only. `includeSubDomains` is what stops an attacker from using a forgotten plain-HTTP subdomain to set a cookie the application's own origin will then send.

`preload` is **not** sent. Preloading is close to irreversible, covers the registrable domain and every subdomain that will ever exist under it, and is an act belonging to the domain's operator rather than to the framework that generated the application. It is also meaningless until the operator submits at hstspreload.org. `WithHSTSPreload()` opts in.

In development the header is not merely omitted — it is deleted from the response if a handler set it. A stray HSTS header pins `http://localhost` in a developer's browser for a year, and nothing the application can do afterwards unpins it.

**`X-Content-Type-Options: nosniff`** stops a browser from ignoring a declared content type and executing a JSON response as script.

**`Referrer-Policy: same-origin`** sends the full URL on the application's own requests and nothing cross-origin. `no-referrer` has identical privacy and strictly less utility: `same-origin` keeps `Referer` available where a request-forgery check may use it as a fallback when `Origin` is absent. An operational application's URLs carry record identifiers and tenant names, which is why no cross-origin-leaking value is the default.

**`Cross-Origin-Opener-Policy: same-origin`** severs the opener relationship with cross-origin windows, closing tab-nabbing and the cross-site leaks that read a victim window's frame count or navigation history.

**`Cross-Origin-Resource-Policy: same-origin`** stops other sites loading this application's responses as subresources. The application is not a content delivery network.

**`Cross-Origin-Embedder-Policy` is not sent.** COEP is a capability enabler for `SharedArrayBuffer` and high-resolution timers, which this kind of application does not use. What it does immediately is refuse every cross-origin subresource that has not opted in — including object-storage attachments behind presigned URLs. `WithCrossOriginEmbedderPolicy` turns it on for an application that has a use for it.

**`Permissions-Policy`** denies every browser capability a generated operational application does not use, to itself and to every embedder. Unrecognized feature tokens are ignored by browsers, so naming a capability a given browser has not shipped costs nothing, while omitting one it *has* shipped leaves it at that browser's own default. `fullscreen=(self)` is the one entry that allows rather than denies: a table or chart going full-screen is ordinary here, and `(self)` still denies it to embedders.

**`X-Frame-Options: DENY`** sits beside `frame-ancestors 'none'`. It is redundant on any browser implementing CSP Level 2 and earns its place only in middleboxes and embedded webviews that strip or fail to parse a CSP header while still honoring it. It is dropped automatically when `AllowFrameAncestors` relaxes the CSP, because `X-Frame-Options` cannot express a list of origins and the two must never disagree.

**`X-XSS-Protection` is not sent.** The legacy auditors it controlled are gone from every browser this project supports, and `1; mode=block` introduced cross-site leaks of its own.

## Inline script: hashes for the shell, nonces for what Go renders

The authenticated frontend is built by `adapter-static` and its HTML is written at build time, so a per-response nonce cannot be baked into it. SvelteKit makes the same call, using hashes rather than nonces for prerendered output.

The built fallback page contains exactly one inline script: SvelteKit's hydration bootstrap. **Its contents, and therefore its hash, change on every build** — the global it defines is named `__sveltekit_<random>` and the modules it imports are content-hashed. A hash written into Go source is wrong after the next `pnpm build`.

So the Go handler that serves the shell computes the hashes from the embedded artifact at startup and passes them in:

```go
hashes, err := webui.InlineScriptHashes() // SHA-256 of each inline <script> in embedded index.html
if err != nil {
    return nil, fmt.Errorf("computing frontend script hashes: %w", err)
}
documentPolicy, err := secure.NewDocumentPolicy(deployment, secure.WithScriptHashes(hashes...))
```

A hash takes no reason and records no waiver, because it names one exact byte sequence: an attacker who injects different content into the same position is still blocked.

Nonces exist for HTML the Go server renders itself — the placeholder page before a frontend build exists, error documents, anything server-rendered later:

```go
nonce, ok := secure.Nonce(r.Context())
if !ok {
    http.Error(w, "server misconfigured", http.StatusInternalServerError)
    return
}
fmt.Fprintf(w, `<script nonce=%q>…</script>`, nonce)
```

Each nonce is 128 bits from `crypto/rand`, minted on the first `Nonce(ctx)` call and identical for every later call on the same request. Minting is lazy on purpose: a handler serving a hashed, immutable build asset never asks, so its response neither spends randomness nor leaves shared caches. A response that *does* carry a nonce is sent `Cache-Control: no-store`, because a nonce a shared cache hands to a second visitor is a nonce an attacker can read and reuse. A handler's own directive is kept only if it already says `no-store` — `private, no-store, max-age=0` survives, `public, max-age=3600` does not.

## What the frontend must do

Three requirements on the generated SvelteKit project, all in `[app]`-owned files:

**Leave `kit.csp` unset in `svelte.config.js`.** For prerendered output SvelteKit emits its policy as a `<meta http-equiv>` tag, where `frame-ancestors` and `report-uri` are ignored. Two policies both apply, as their intersection, and nobody debugging a blocked resource should have to find a second one hiding in the markup. The Go response header is the single authoritative policy.

**Leave `kit.inlineStyleThreshold` at its default of 0.** Raising it inlines stylesheets into the page, which would need hashes in `style-src`.

**Write no literal `style` attribute** in `app.html` or in a component. Use a class or the `style:` directive; `style:` compiles to CSSOM property writes, which CSP does not govern. Svelte 5 transitions are safe — they animate through the Web Animations API, which CSP does not govern either. (The SvelteKit configuration docs still say transitions require inline styles. That describes Svelte 4 and is stale.)

**Prefer a stylesheet rule to Svelte's component-level custom-property syntax.** Writing `<Component --token="value" />` makes Svelte wrap the component in a `<svelte-css-wrapper>` element carrying `style="display: contents; --token: value"`. That is an inline style attribute, so it is reported under a policy without `style-src-attr 'unsafe-inline'`. This matters because CSS custom properties are the theming primitive for generated applications, so it is exactly the syntax an author reaches for.

Measured on the probe build: **the theming still works** — the token resolves and the wrapper still computes to `display: contents` — because Svelte applies both through CSSOM after creating the element, and CSP does not govern CSSOM writes. What it costs is **two `style-src-attr` violation reports per component instance**, scaling linearly: a page with three such components reported six, plus the announcer's two.

So this is a report-volume problem, not a broken-theming problem. Do **not** reach for `AllowInlineStyleAttributes` to silence it — that permanently weakens the policy of every generated application in order to quiet reports about something that is working. Set the custom property from a stylesheet rule or a class instead:

```css
/* app.css — a token scoped by class, no inline style attribute */
.theme-brand { --accent: oklch(55% 0.2 264); }
```

```svelte
<Component class="theme-brand" />
```

Measured on Svelte 5.56.10, and **not** confirmed against the M6 shell, which may use the wrapper syntax in components Nise generates. It is part of what the M6 test below has to settle.

## The one residual

SvelteKit's client runtime creates its own accessibility announcer, `<div id="svelte-announcer">`, with a hard-coded inline `style` attribute holding the standard visually-hidden declarations. It is framework code; an application cannot remove it, and the strict policy blocks the attribute. That produces two `style-src-attr` violation reports per page load.

This is the only residual with a *functional* consequence, and only if it is left unhandled. The `<svelte-css-wrapper>` reports described above are noise rather than breakage, and any literal `style` attribute an application writes itself is avoidable.

Nothing is functionally or visually wrong, provided the generated `frontend/src/app.css` restores the geometry from the external stylesheet — which `style-src 'self'` allows:

```css
/* SvelteKit sets these as an inline style attribute on its accessibility
   announcer. A CSP without style-src-attr 'unsafe-inline' blocks that, so
   restore them here; otherwise the announcer's route names render visibly
   on every client-side navigation. */
#svelte-announcer {
  position: absolute !important;
  left: 0 !important;
  top: 0 !important;
  clip: rect(0 0 0 0) !important;
  clip-path: inset(50%) !important;
  overflow: hidden !important;
  white-space: nowrap !important;
  width: 1px !important;
  height: 1px !important;
}
```

This was verified in a browser: with the rule present and the attribute blocked, the announcer is correctly hidden and its text does not appear in the page.

The alternative is `AllowInlineStyleAttributes(reason)`, which adds `'unsafe-inline'` to `style-src-attr` — inline style attributes on elements, still not inline `<style>` elements. It is a recorded weakening. Prefer the stylesheet rule; reach for the waiver only if a component library turns out to need attributes the stylesheet cannot cover.

## Changing the policy

`Policy` has no exported fields and no setters. Every change is an option, and the names carry the property:

- **`With…` never weakens anything.** It tightens a control, switches on a rollout mechanism, or names exact content by hash.
- **`Allow…` weakens something.** It takes the reason as its first argument, refuses an empty reason, and records a `Waiver` on the policy.

There is no option that removes a header, and no option can produce `'unsafe-eval'`, `'unsafe-inline'` in `script-src`, `'unsafe-hashes'`, `'wasm-unsafe-eval'`, or `*`. Those are refused by the shared source validator, so the weakening does not exist in the API. The middleware also writes the headers at response-commit time and overwrites what is there, so a handler cannot delete or downgrade one afterwards.

A weakened policy is therefore visible twice: in the application's own source, as a call whose name starts with `Allow`; and at runtime, through `Policy.Waivers()`.

```go
for _, w := range documentPolicy.Waivers() {
    logger.Warn("security policy weakened", slog.String("waiver", w.String()))
}
```

A test can require that there are none:

```go
if got := documentPolicy.Waivers(); len(got) != 0 {
    t.Fatalf("policy carries %d security waivers: %v", len(got), got)
}
```

### The options

| Option | Effect | Waiver |
|---|---|---|
| `AllowReportOnly(reason)` | Report violations, block nothing at all | yes |
| `WithReportURI(uri)` | Send reports to a same-origin absolute path | no |
| `WithScriptHashes(…)` | Allow exact inline scripts by hash | no |
| `WithStyleHashes(…)` | Allow exact inline style elements by hash | no |
| `WithHSTSPreload()` | Add `preload` to HSTS | no |
| `WithReferrerPolicy(p)` | `no-referrer` or `same-origin` only | no |
| `WithCrossOriginEmbedderPolicy(p)` | Send COEP | no |
| `AllowScriptSources(reason, …)` | Add origins to `script-src` | yes |
| `AllowStyleSources(reason, …)` | Add origins to `style-src` | yes |
| `AllowConnectSources(reason, …)` | Add origins to `connect-src` | yes |
| `AllowImageSources(reason, …)` | Add origins to `img-src` | yes |
| `AllowFontSources(reason, …)` | Add origins to `font-src` | yes |
| `AllowFrameAncestors(reason, …)` | Permit framing; also drops `X-Frame-Options` | yes |
| `AllowInlineStyleAttributes(reason)` | `style-src-attr 'unsafe-inline'` | yes |
| `AllowCrossOriginPopups(reason)` | COOP `same-origin-allow-popups` | yes |
| `AllowCrossOriginResources(reason, p)` | CORP `same-site` or `cross-origin` | yes |
| `AllowReferrerPolicy(reason, p)` | A referrer policy that leaks cross-origin | yes |
| `AllowExternalReportURI(reason, uri)` | Send reports to an https collector at another origin | yes |
| `AllowPermission(reason, feature, …)` | Grant a denied browser capability | yes |
| `AllowReducedHSTS(reason, maxAge, includeSubdomains)` | Shorten HSTS or drop `includeSubDomains` | yes |

Options that only make sense for a document policy fail construction on an API policy rather than silently doing nothing. Construction errors accumulate, so several bad options are reported together.

## Rolling out a policy change

`AllowReportOnly(reason)` sends the policy in `Content-Security-Policy-Report-Only`, so violations are reported and nothing is blocked. The enforcing header and the report-only header are never sent together: whichever the policy is, the other is deleted from the response.

It is an `Allow` option rather than a `With` one because it does not weaken a directive — it suspends the whole CSP. A report-only policy that reaches production and stays there looks in every other respect like a strict one, so it is recorded as a waiver like any other weakening.

The rollout:

1. Build the candidate policy with `AllowReportOnly(reason)` and `WithReportURI("/api/v1/csp-reports")`, and deploy it to a canary alongside the enforced current policy.
2. Collect for long enough to cover the screens people use rarely — enrollment, TOTP setup, upload management, the admin pages. A CSP break in a monthly workflow does not show up in a day.
3. Read the reports, fix the application or add the narrowest option that covers the gap.
4. Remove `AllowReportOnly`.

To enforce one policy while reporting a stricter candidate, build two policies and mount both middlewares.

## Wiring

```go
deployment, err := secure.ParseDeployment(string(cfg.Environment))
if err != nil {
    return nil, err
}

documentPolicy, err := secure.NewDocumentPolicy(deployment, secure.WithScriptHashes(hashes...))
if err != nil {
    return nil, err
}
apiPolicy, err := secure.NewAPIPolicy(deployment)
if err != nil {
    return nil, err
}

mux.Handle("/api/v1/", secure.Middleware(apiPolicy)(apiRouter))
mux.Handle("/", secure.Middleware(documentPolicy)(webuiHandler))
```

Mount `secure.Middleware` as the outermost middleware in each chain, so a response produced by an inner middleware — a rejected request, a rate-limit refusal, a recovered panic — carries the headers too. The ordered chain lives in the application's own `internal/platform/httpapi/router.go` ([ADR 0009](adr/0009-generated-application-layout.md)), which is where the order is meant to be read and changed.

`ParseDeployment` accepts `development`, `test`, and `production`, mapping `test` to development. An unrecognized value is an error, so a typo fails at startup rather than silently selecting the weaker policy.

## Confirming the policy when the application shell exists

The evidence in [ADR 0013](adr/0013-security-headers-and-csp.md) comes from a representative SvelteKit 2 + `adapter-static` + Svelte 5 + Tailwind v4 build, driven in a browser under the enforced production policy. It is not the milestone-6 application shell, which adds shadcn-svelte/Bits UI, TanStack Table, and Paraglide. Those are expected to be invisible to CSP — Floating UI positions through CSSOM writes, which CSP does not govern — but that was not observed, and this page does not claim it.

The test that closes it, to be added to the generated application's end-to-end suite when the shell exists:

> Serve the built shell from the application binary with the production document policy **enforced** (not report-only). In the browser, register a `securitypolicyviolation` listener **before the first navigation**. Sign in and exercise the shell: sidebar collapse, the mobile drawer, a dialog, a dropdown menu, a tooltip, a toast, a data table's sort and pagination, the theme toggle, and the language switch. Then assert:
>
> 1. **No recorded violation names a `violatedDirective` other than `style-src-attr`.** Any `script-src`, `style-src`, `style-src-elem`, `connect-src`, `img-src`, or `font-src` violation is a failure.
> 2. **Every `style-src-attr` violation is attributable to framework-generated markup** — SvelteKit's `#svelte-announcer`, or a `<svelte-css-wrapper>` from the component custom-property syntax — and none to a literal `style` attribute in a file Nise generated.
> 3. **The theme actually applies**, asserted on computed style rather than on the absence of violations: read a token-driven color off a rendered component and require the token's value, not the `var()` fallback. This is the assertion that would catch a future Svelte version applying the wrapper styles through the parsed attribute instead of through CSSOM, which would turn today's harmless report into silently broken theming.
> 4. **Record the violation count.** It should be a small constant plus two per `<svelte-css-wrapper>` instance. A count that grows with interaction means something re-renders an inline style attribute on every update, which is a report-volume problem worth fixing at the source.

A failing assertion means one of three things, and the test should say which: a component library needs inline style attributes, in which case `AllowInlineStyleAttributes` becomes a documented default with a recorded waiver; the theming assertion failed, which is a Svelte behavior change and a defect to fix in the components rather than a policy to relax; or something reintroduced inline script or `eval`, which is a defect to fix.

A cheap static guard is worth adding alongside it: fail generation, or `nise check`, if a generated `.svelte` file or `app.html` contains a literal `style="` attribute.
