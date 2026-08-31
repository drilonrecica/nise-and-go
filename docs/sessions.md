# Sessions

Generated applications authenticate browsers with opaque, server-side sessions
stored in PostgreSQL. There are no JWTs anywhere
([ADR 0006](adr/0006-opaque-sessions-no-jwt.md)).

## The token

A session token is 32 bytes of cryptographic randomness and nothing else:

```
ns1_<43 characters of unpadded base64url>
```

It says nothing about who holds it, when it was issued, or what it permits.
Every one of those answers comes from the row the server looks up, which is
what makes revocation immediate rather than a wish.

**Only the digest is stored.** The server keeps SHA-256 of the token, never the
token. A leak of the sessions table hands nobody a working credential, and a
backup that captures it captures nothing replayable. The digest is a plain hash
rather than a keyed one because the token is 256 bits of uniform randomness:
there is no dictionary to search and nothing for a pepper to defend. Password
hashing is the opposite case and uses [Argon2id](passwords.md).

`session.Token` is unprintable through every formatting and serialization path
Go offers — `String`, `GoString`, `MarshalJSON`, `MarshalText`, and `LogValue`
all return a placeholder. `Token.Value()` is the single explicit accessor, for
the one moment the value is handed to the client.

## Two clocks, both stored

| Bound | Default | Enforced from |
|---|---:|---|
| Idle | 12 hours | the row's recorded activity time |
| Absolute | 30 days | the deadline written when the session was created |

The absolute deadline is written at issue rather than recomputed from
configuration, so **shortening the configured lifetime does not silently extend
sessions that already exist, and lengthening it does not either**. That is a
property an operator responding to an incident depends on.

`Record.Evaluate` reports the strongest reason a session is unusable, in the
order revoked, expired, idle. A revoked session that has also expired is
revoked: that is what an operator asked for and what an audit record should
say.

## Touching

Sliding the idle window on every request would turn every authenticated `GET`
into a database write. A session's activity time is written back only once the
recorded value is older than the touch interval (5 minutes by default), which
bounds that cost without meaningfully changing when a session expires. The
touch happens in the same transaction as the lookup, so a session revoked
concurrently cannot be revived by an in-flight request.

## Rotation

`Rotate` replaces a session's token with a new one and ends the old session in
the same transaction, so there is no instant in which both work. It is the
answer to a possibly-stolen credential at a moment the server knows the holder
is genuine: after a password change, after reauthentication, after a privilege
change.

**Rotation copies the absolute deadline; it never recomputes it.** Recomputing
would make repeated rotation an unbounded session, which is the exact bound the
absolute deadline exists to impose. A test rotates twenty times across a day and
asserts the session still ends when it was always going to.

**Periodic rotation on a timer is deliberately not offered.** A browser making
two requests at once would rotate twice, and one of them would be left holding a
token that was already replaced — logging the user out for being active.
Rotation belongs at moments the application chooses, not on a clock.

## Revocation

| Operation | What it does |
|---|---|
| `Revoke` | Ends one session. Repeating it fails rather than overwriting the first reason. |
| `RevokeOtherSessions` | "Sign out everywhere else" — every session but the current one. |
| `RevokeAllForUser` | Every session the account holds. Unchecked: the callers are the account acting on itself and system paths with no authenticated caller. |
| `RevokeForAccount` | Every session *another* account holds, requiring `sessions.revoke`. The administrative response to a compromise. |
| `Rotate` | A new token for the same session, ending the old one. |
| `DeleteExpired` | Removes a bounded batch of sessions past their absolute deadline. |

Every revocation records a reason, bounded to 64 bytes, which is what an audit
record and the account's own session list have to show.

Disabling an account and revoking its sessions are deliberately separate. One is
reversible; the other logs the person out of every device *immediately*, where
disabling takes effect on each session's next request. A disabled account's
refusal does not slide the idle window on the way past.

## Storage

`db/migrations/00003_identity.sql` creates `users` and `sessions`. Every rule
the application enforces is repeated as a database constraint — normalized
address, digest width, `expires_at > created_at`, revocation reason present
exactly when a revocation time is — because a bug in application code must not
be able to write a row the rest of the system cannot reason about.

The address is stored already lowercased, so lookups are a plain equality match
on a plain index and two rows differing only in case cannot both exist. Only
case and surrounding whitespace are normalized: stripping dots or plus-tags
would merge addresses their owners consider distinct, and would do it
differently from whatever mail server actually delivers to them.

## The auth feature

`internal/features/auth` is the first real feature and the worked example of
the [feature-colocated sqlc](adr/0014-feature-colocated-sqlc.md) layout:

| Path | Owner | Purpose |
|---|---|---|
| `queries/*.sql` | application | hand-written SQL, input to sqlc |
| `store/` | sqlc | complete generated output, never hand-edited |
| `sqlc.yaml` | application | generation config, pointing at `db/migrations` |
| `accounts.go` | application | identity use case |
| `sessions.go` | application | session use case, owning its transactions |
| `auth_test.go` | application | real-PostgreSQL contracts |

The sqlc config points at the migration directory rather than a copy of the
schema. A second schema file would drift from the one PostgreSQL actually runs,
and the drift would surface as generated code compiling against a table shape
that does not exist.

Neither `queries/` nor `store/` carries an ownership header comment; both
declare ownership by path. A comment in a query file is not inert — sqlc copies
it into the generated Go doc comment — so a "Nise will not overwrite it" header
there would appear inside a tool-owned file and contradict its own marker.

## The cookie

| Attribute | Value |
|---|---|
| Name | `__Host-session` |
| `Secure` | yes |
| `HttpOnly` | yes |
| `SameSite` | `Lax` |
| `Path` | `/` |
| `Domain` | never set |

The `__Host-` prefix is browser-enforced: a browser refuses such a cookie unless
`Secure` and `Path=/` are set and `Domain` is not, which is what stops a sibling
subdomain — or a network attacker forging a response from one — from writing a
session cookie this application would then read.

**Development uses the same cookie.** `http://localhost` and `http://127.0.0.1`
are secure contexts in every current browser, so a `Secure` cookie works there
over plain HTTP and a developer exercises the production code path. There is no
environment-conditional policy to get wrong.

The one escape hatch is for a development origin that is *not* localhost — a
phone on the LAN pointed at a workstation. `SESSION_COOKIE_INSECURE=true` drops
`Secure` and the `__Host-` prefix together, because a browser discards a
`__Host-` cookie that is not `Secure` and the pair cannot be split. It warns at
startup and is refused in production. See
[ADR 0018](adr/0018-development-cookie-policy.md) for why `SameSite` is `Lax`
rather than `Strict`.

## At the HTTP boundary

`internal/platform/httpauth` carries the session between the browser and the
application:

- `Cookies` writes, reads, and clears the cookie. `Clear` matches every
  attribute `Write` sent except the value and lifetime, or the browser treats it
  as a different cookie and leaves the original in place. A request carrying two
  cookies with the session's name is treated as carrying **no** credential
  rather than resolved by picking whichever arrived first.
- `Resolver.Middleware` resolves a valid cookie into a request-scoped identity
  and **never rejects a request**. An absent, malformed, expired, or revoked
  credential simply produces an unauthenticated request; the use case behind the
  handler refuses it. Rejecting in middleware would put an authorization
  decision where no handler's reader can see it, and would make every public
  endpoint a special case.
- A credential that was presented and did not resolve has its cookie cleared, so
  a browser holding a dead session stops sending it — which also stops a dead
  credential from costing a database lookup on every request.
- The context key is unexported, so nothing outside the package can place an
  identity in a request. `FromContext` returns two values, so a caller has to
  acknowledge the unauthenticated case rather than receive a zero-valued record
  whose empty `UserID` would compare equal to another empty one.

The refusal reason is logged, never the credential. An error may implement
`SessionRefusalReason() string` to name itself in one bounded word, which is how
`auth.InactiveError` reports `revoked`, `expired`, or `idle` without this
package importing the feature.

## Anti-forgery

State-changing requests pass three checks, and all of them must pass. They are
layered because each fails differently:

1. **Fetch Metadata.** A browser sets `Sec-Fetch-Site` and a page cannot change
   it, so this is the one check an attacker cannot satisfy from another site at
   all. `same-site` is refused along with `cross-site`: a sibling subdomain is a
   different security boundary, which is exactly why the session cookie carries
   `__Host-`. An absent header is not a refusal — a non-browser client does not
   set one — and the remaining checks still apply.
2. **Origin.** With no explicit list, the comparison is against the
   application's own resolved origin. A browser making a cross-site request
   sends the attacker's `Origin` and this application's `Host`, so the two
   disagree. `Referer` is a fallback for a browser that stripped `Origin`, and
   only its origin is read. Neither present is a refusal: a browser sends
   `Origin` on every state-changing request.
3. **The session-bound token.** When the request carries a session, an
   `X-CSRF-Token` header must carry the token bound to *that* session.

The token is derived, not stored:

```
token = base64url(HMAC-SHA256(key = session token, "nise-csrf-v1"))
```

Using the session token as the key means there is no server-side key to
configure, no column to migrate, and no separate lifetime: the token changes
exactly when the session does, and one session's token cannot verify against
another. The derivation is one-way, so handing the value to the browser reveals
nothing about the credential.

It is delivered in `__Host-session_csrf`, which shares every attribute of the
session cookie **except** `HttpOnly` — the application's own script has to read
it to echo it in a header, and that is the entire mechanism. Handing it to
script costs nothing an attacker does not already have after cross-site
scripting, and buys the property that matters: a form posted from another origin
cannot set a request header.

The resolver keeps the pair in step. A browser missing the anti-forgery cookie,
or holding one from a session that has since rotated, gets the current value
back on its next resolved request rather than having to sign in again; a
credential that fails to resolve has both cookies cleared together.

**Unauthenticated state changes are still protected.** Logging in is the case
login CSRF targets, and there is no session to bind a token to; Origin and Fetch
Metadata carry it.

Every refusal is one public code — `403 cross_site_request` on the API surface —
because which of a caller's guesses was structurally valid is not something to
tell them. Which check failed is in the server's log.

## Not yet here

Login and enrollment are the tasks that follow. Resolving a session says who is
asking; nothing here decides what they may do, and there is no login endpoint to
obtain a session from yet.

## Related

- [Password hashing](passwords.md)
- [Security model](security.md)
- [Database queries and sqlc](database-queries.md)
- [Runtime packages](runtime-packages.md)
