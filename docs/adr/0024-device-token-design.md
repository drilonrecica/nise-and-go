# 0024: Design native device tokens now; expose nothing yet

- **Status:** Accepted
- **Date:** 2026-08-31
- **Extends:** [ADR 0006](0006-opaque-sessions-no-jwt.md)

## Context

The blueprint has said from the beginning that future native clients will
authenticate with opaque, hashed, revocable bearer device tokens against the
same session model, and never with JWTs. No native client exists, and V0.1 ships
none.

Designing it now is not speculation about a feature; it is checking that today's
browser-only decisions do not quietly make tomorrow's native client a breaking
change. Three of them could: the session credential's shape, the anti-forgery
guard's structure, and where the "who is asking" answer is resolved. Each is
cheap to keep compatible and expensive to retrofit.

This ADR records the design and the compatibility findings. **Nothing here is
implemented**: no runtime package, no table, no configuration, no endpoint. A
generated V0.1 application has exactly one credential type for its API, and that
is the session cookie.

## Decision

### Device tokens are a separate credential over the same session model

A device token is a long-lived bearer credential belonging to one installation
of a native application on one device. It is **not** a session token with a
different transport:

| | Session | Device token |
|---|---|---|
| Belongs to | one browser sign-in | one installation on one device |
| Lifetime | 12 h idle / 30 d absolute | months, bounded, renewable |
| Transport | `__Host-` cookie | `Authorization: Bearer` |
| Revoked by | signing out, admin action | signing out *that device*, admin action |
| Produces | the request's identity | a short-lived session, on demand |

The last row is the design. A device token does **not** authenticate a request.
It exchanges itself for an ordinary session, and the session authenticates the
request exactly as a browser's does. Everything already built — the idle and
absolute clocks, revocation, `proven_at` and the reauthentication matrix, the
audit trail, the throttle — then applies to native clients without a second
implementation, and there is no second code path that could be the one somebody
forgets to check `revoked_at` in.

It also means a stolen device token is worth what a stolen refresh token is
worth and no more: it must be exchanged, and the exchange is a request the
server can throttle, audit, and refuse.

### Its own table and its own prefix

```sql
CREATE TABLE device_tokens (
    id            uuid PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_digest  bytea NOT NULL,        -- SHA-256, never the token
    label         text NOT NULL,         -- what the person sees in their device list
    created_at    timestamptz NOT NULL,
    last_used_at  timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    revoked_reason text
);
```

A separate table, not a column on `sessions`, and a separate token type with its
own prefix (`dev1_`). The rule this project already applies to session,
invitation, and second-factor challenge tokens applies here for the same reason:
a credential that can be presented in two places is a credential that will be,
and a lookup against the wrong table must not compile.

Only the digest is stored, as everywhere else. A plain SHA-256, because the
token is 256 bits of uniform randomness with no dictionary to search.

### Issuing one is a deliberate act, never a side effect of signing in

A device token is issued only by an explicit "remember this device" request from
an already-authenticated session that has a **recent proof of identity** — an
entry in the [reauthentication matrix](0021-reauthentication-matrix.md), on that
matrix's own principle: it creates a way in that outlives the request that made
it, which is exactly what an invitation does.

The token is returned once, in the response, and is not recoverable afterwards.
The label is supplied by the client and shown to the person in their own device
list, which is what makes "revoke the one I lost" a thing somebody can actually
do.

### Exchange is rotating and single-use

Exchanging a device token issues a session **and** replaces the device token,
returning the new one, in one transaction. A token that survived its own use
would be a credential an attacker could keep replaying from a copy taken months
ago; rotating it means a stolen copy works at most once, and the moment the real
device presents the token it no longer holds, the server knows one of the two
was cloned.

Presenting a *retired* device token is therefore not merely a refusal: it is
evidence of a clone. The response is to revoke the whole chain for that device
and record it, which is the standard refresh-token-reuse detection and the one
piece of this design that has to be built with the rest rather than added later.

Rotation copies the absolute deadline rather than recomputing it, exactly as
session rotation does, so repeated exchange is not an unbounded credential.

### Bearer requests are outside the anti-forgery guard — decided by how the request authenticated

A browser cannot be made to attach an `Authorization` header cross-site, so a
request authenticated by a bearer token needs no CSRF token. The dangerous way
to implement that is a guard that skips its checks when an `Authorization`
header is *present*: an attacker's cross-site form cannot set a header, but a
`fetch` with `credentials: "include"` and an empty or junk `Authorization`
header would then bypass the guard while the request still authenticated by
cookie.

The rule is therefore: **the guard's decision follows how the request was
actually authenticated, not what headers it carries.** A request whose identity
came from the cookie is checked; a request whose identity came from a bearer
token is not; a request with no identity is carried by Origin and Fetch Metadata
as it is today.

Today's `Guard.refusal` already reads the resolved credential rather than
sniffing headers, and returns early only when *the cookie* is absent. Extending
it means the resolver reporting which credential answered, not restructuring the
guard.

### The resolver gains a source, not a second identity

`httpauth.Resolver` resolves a cookie into `session.Record` and never rejects.
Adding a bearer source is additive: the same middleware, the same context key,
the same `session.Record`, with one more field recording which credential
resolved it. Everything downstream — authorization, reauthentication, audit —
keeps reading one identity from one place.

Two credentials on one request is a client error, not a fallback. Resolving both
and preferring one would mean an attacker who can set a header gets to choose
which identity the request has.

### Native clients get the second factor for free, and a device is not one

The exchange endpoint runs the same sign-in path, so an account with a second
factor enrolled is challenged when it *creates* a device token, and never
afterwards — the device token is the memory of that completed sign-in. A device
token is emphatically not a second factor and must never be treated as one.

### What is deliberately not decided here

- **Certificate or key attestation.** Binding a token to a platform keystore
  is a real improvement and a per-platform one; it is an addition to this
  design, not a change to it.
- **Push-based revocation.** Revocation is immediate server-side because the
  exchange is a server round trip; telling a device it has been revoked before
  its next request is a notification problem.
- **Offline access.** There is none. A device token buys a session, and a
  session needs the server.

## Compatibility findings

Three things had to be true for this to remain additive, and all three are:

1. **The session token's shape is prefixed and versioned** (`ns1_`), so a second
   credential type is a different prefix rather than an ambiguous parse.
2. **The anti-forgery guard reads the resolved credential**, not the presence of
   a header, so the bypass described above is not reachable by adding a source.
3. **Identity is resolved once, in middleware, into one context value**, so a
   second credential source adds a branch in one place rather than a second
   notion of "who is asking" that the rest of the application would have to
   learn.

Nothing in V0.1 needs to change to keep them true. This ADR exists so that a
change which would break one of them is recognised as breaking it.

## Alternatives considered

- **JWT access tokens with refresh.** Rejected by [ADR 0006](0006-opaque-sessions-no-jwt.md), and this design is the reason that rejection is affordable: an opaque device token exchanged for a session gives the same offline-free ergonomics with revocation that is a `DELETE`, not a wish.
- **Let the device token authenticate requests directly.** Rejected: it duplicates every session rule in a second code path, and the second path is the one that ends up missing a check.
- **Store device tokens in `sessions` with a `kind` column.** Rejected: one table for two credential types is how a lookup against the wrong one starts compiling, and the two have genuinely different lifetimes and revocation semantics.
- **Non-rotating device tokens.** Rejected: a long-lived credential that survives its own use gives a stolen copy indefinite value and gives the server no way to detect the theft.
- **Implement it now, behind a flag.** Rejected: an unused authentication path is an attack surface nobody is exercising, and a flag is not a compile-time absence. When a native client exists, this becomes a compile-time module ([ADR 0022](0022-compile-time-modules.md)).

## Consequences

- V0.1 ships one API credential. A reader of the code finds one, and a security
  review has one to review.
- The work to add device tokens is a table, a token type, an exchange use case
  with reuse detection, one resolver source, one matrix entry, and a device list
  — with no change to the session model, the guard's structure, or the identity
  the rest of the application reads.
- If a future change makes the guard sniff headers, collapses the two token
  types, or resolves identity in more than one place, this ADR is what says what
  it costs.
