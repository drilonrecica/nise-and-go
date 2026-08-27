// Package secure provides the security response headers and the Content
// Security Policy a generated application sends, for the embedded
// single-page application and for its API.
//
// Build a [Policy] with [NewDocumentPolicy] or [NewAPIPolicy], hand it to
// [Middleware], and mount that as the outermost middleware in the chain.
// [Nonce] gives an HTML handler the per-response nonce for the inline scripts
// it emits.
//
// # default-src 'none', and everything after it is an argument
//
// The base of the document policy is default-src 'none': nothing loads, from
// anywhere, until a directive says otherwise. Each directive that follows is
// there because something in a generated application needs it, and each one
// is justified in docs/security-headers.md and in ADR 0013 rather than
// inherited from a boilerplate policy.
//
// There is no 'unsafe-inline' and no 'unsafe-eval' in script-src, in any
// deployment, and no option in this package can put one there. Inline script
// is allowed by nonce ([Nonce]) or by hash ([WithScriptHashes]) — by naming
// the exact script, never by opening the directive.
//
// # Nonces and hashes, and why a static frontend needs both
//
// The authenticated frontend is a SvelteKit application built with
// adapter-static and embedded in the binary (ADR 0005). Its HTML is written
// at build time, so a per-response nonce cannot be baked into it; SvelteKit
// makes the same call, using hashes rather than nonces for prerendered
// output. The Go server therefore computes the hash of the inline bootstrap
// script from the embedded artifact at startup and passes it to
// [WithScriptHashes]. That hash changes on every frontend build, so it must
// be derived from the built file rather than written into Go source.
//
// [Nonce] exists for the HTML the Go server renders itself — the placeholder
// page before a frontend build exists, an error document, anything a later
// milestone renders server-side. It mints from crypto/rand, per response, on
// first use.
//
// Minting is lazy on purpose. A handler serving a hashed, immutable build
// asset never calls [Nonce], so its response neither spends randomness nor
// leaves shared caches; a response that does carry a nonce is sent with
// Cache-Control: no-store, because a nonce a shared cache hands to a second
// visitor is a nonce an attacker can read and reuse.
//
// # Configuration cannot silently remove a protection
//
// [Policy] has no exported fields and no setters. Every adjustment is an
// [Option], and the option names carry the safety property:
//
//   - With… never weakens anything.
//   - Allow… weakens something, takes the reason as its first argument,
//     refuses an empty reason, and records a [Waiver] the application can
//     read back from [Policy.Waivers].
//
// So a weakened policy is visible in the application's own source as a call
// whose name starts with Allow, and visible at runtime as a waiver an
// application can log at startup or assert against in a test. There is no
// option that deletes a header, and the middleware overwrites the policy's
// headers at response-commit time, so a handler further down the chain cannot
// quietly drop one either.
//
// # Two policies, not one
//
// [NewAPIPolicy] is strictly stricter than [NewDocumentPolicy]. Its CSP names
// no script-src, style-src, img-src, or connect-src at all, and adds the
// sandbox directive, so an API response a browser is talked into rendering as
// a document has no origin and runs nothing. Mount it on the API subtree and
// the document policy on everything else.
//
// # Decisions worth knowing about
//
// Strict-Transport-Security is sent only in a [Production] deployment, with
// includeSubDomains and a one-year max-age, and without preload.
// [WithHSTSPreload] explains why preload is opt-in. In [Development] the
// header is not merely omitted but actively deleted, because a stray HSTS
// header pins http://localhost in a developer's browser for a year with no
// way for the application to unpin it.
//
// Cross-Origin-Embedder-Policy is not sent. It is a capability enabler for
// SharedArrayBuffer and high-resolution timers, which this kind of
// application does not use, and it breaks cross-origin subresources that have
// not opted in — which in a Nise application means object-storage attachments.
// [WithCrossOriginEmbedderPolicy] turns it on for an application that has a
// use for it.
//
// X-Frame-Options: DENY is sent alongside frame-ancestors 'none', and dropped
// when [AllowFrameAncestors] relaxes the CSP. It is redundant on every
// browser that implements CSP Level 2, and it earns its 22 bytes only in the
// middleboxes and embedded webviews that strip or fail to parse a CSP header
// but still honor it. What matters more is that the two never disagree, which
// is why one option drives both.
//
// Referrer-Policy is same-origin. It leaks nothing cross-origin, and unlike
// no-referrer it keeps the Referer header available for the application's own
// same-origin requests, where a request-forgery check may use it as a
// fallback when Origin is absent.
//
// # What is verified and what is not
//
// The policy's inline-content claims were checked against a real build of
// SvelteKit 2 with adapter-static, Svelte 5, and Tailwind v4, served under
// this exact policy and exercised in a browser: the bundle contains no eval
// or Function constructor, Tailwind's output is an external stylesheet,
// Svelte 5 transitions animate through the Web Animations API and raise no
// style-src violation, and the only inline style attribute that appears comes
// from SvelteKit's own accessibility announcer, whose effect an application
// stylesheet can reproduce.
//
// That build is not the milestone-6 application shell, which adds a component
// library, a table library, and internationalization. ADR 0013 records what
// must be re-verified when it exists, and docs/security-headers.md describes
// the test that will do it.
package secure
