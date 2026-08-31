# 0018: Development uses the same hardened session cookie as production

- **Status:** Accepted
- **Date:** 2026-08-31
- **Closes:** the `THREAT_MODEL` open question on `__Host-`/`Secure` behavior over plain-HTTP localhost (M5-015)

## Context

The production session cookie is `__Host-`-prefixed, `Secure`, `HttpOnly`,
`SameSite=Lax`, `Path=/`, with no `Domain`. The `__Host-` prefix is not
decoration: a browser refuses such a cookie unless all three of `Secure`,
`Path=/`, and no `Domain` hold, which is what stops a sibling subdomain — or a
network attacker who can forge a response from one — from writing a session
cookie the application will then read.

The development loop serves plain HTTP. The obvious reading is that `Secure`
cannot work there, and the usual response is a conditional: drop the prefix and
the attribute when `APP_ENV=development`. That response has a well-known cost.
The code path a developer exercises every day stops being the code path that
runs in production, so a cookie bug in the hardened branch is found by users
rather than by the person writing it. It also creates a switch whose accidental
truthiness in production is a silent downgrade — exactly the failure mode
[ADR 0013](0013-security-headers-and-csp.md) took pains to avoid for CSP.

The obvious reading is also wrong. Every current browser treats `http://localhost`
and `http://127.0.0.1` as **secure contexts** (W3C Secure Contexts, "potentially
trustworthy origin"; Chrome since 2016, Firefox since 75, Safari, Edge). A
`Secure` cookie is set and sent on those origins over plain HTTP. The dev loop
([ADR 0012](0012-dev-loop-proxy-topology.md)) already serves the browser one
origin on localhost, which is where this applies.

## Decision

### Development is production

There is no environment-conditional cookie policy. A generated application
builds one `session.CookiePolicy`, and on both a production host and a
developer's `localhost` it is the same one: `__Host-session`, `Secure`,
`HttpOnly`, `SameSite=Lax`, `Path=/`, no `Domain`. The hardened branch is the
only branch anybody runs.

### One explicit escape hatch, for one real case

A developer whose browser is not on localhost — a phone on the LAN pointed at a
workstation, a hostname mapped to a dev machine — is not in a secure context,
and the cookie will simply not be set. For them, and only them,
`SESSION_COOKIE_INSECURE=true` drops `Secure` **and** the `__Host-` prefix
together, naming the cookie `session`.

The two cannot be separated, and the policy refuses to try: a `__Host-` cookie
without `Secure` is discarded by the browser, and an unprefixed `Secure` cookie
silently gives up a browser-enforced guarantee while looking fine. Asking for
one and getting the other is a construction error, not a correction.

The escape hatch is:

- **refused in production**, through the same fail-closed validator that already
  refuses `DEBUG=true` and an unset signing key there;
- **warned about at startup**, in every environment, naming the variable;
- **absent by default**, so nobody reaches it by not thinking about it.

### `SameSite=Lax`, not `Strict`

`Strict` withholds the cookie on a top-level navigation from any other site, so
following a link from an email arrives at a logged-out page and the user logs in
again — which trains people to type their password after clicking a link in a
message. `Lax` plus a session-bound CSRF token plus Origin and Fetch Metadata
checks is the design (M5-003). `Strict` is not a substitute for any of them, and
on its own it is not a CSRF defense.

### What is excluded

- No `Domain` attribute, ever. `__Host-` forbids it, and a session shared across
  subdomains is a session any one of them can steal.
- No environment-sniffing inside the policy. It is told whether the transport is
  secure; a policy object that guessed from a hostname would be a second,
  divergent source of truth about the environment.
- No separate "development cookie name" as a default. The name changes only on
  the escape hatch, because that is the one case where it must.

## Consequences

- A developer on `localhost` runs the production cookie path, so a mistake in it
  shows up on their machine rather than in production.
- A developer who wants to test from a phone on the LAN has to set one variable
  and will see a warning until they unset it. That is the intended friction.
- The generated application must refuse to start in production with the escape
  hatch set. That check is part of M5-002's configuration, not of this package.
- If a browser ever stops treating `http://localhost` as a secure context, the
  default breaks loudly — the cookie is not set and login does not work — rather
  than quietly weakening. That is the failure direction to prefer, and it is a
  reason to revisit this ADR rather than to have hedged now.
