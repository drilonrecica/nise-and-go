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

## Revocation

| Operation | What it does |
|---|---|
| `Revoke` | Ends one session. Repeating it fails rather than overwriting the first reason. |
| `RevokeOtherSessions` | "Sign out everywhere else" — every session but the current one. |
| `RevokeAllForUser` | Every session, which is what a password change and a compromise response both need. |
| `DeleteExpired` | Removes a bounded batch of sessions past their absolute deadline. |

Every revocation records a reason, bounded to 64 bytes, which is what an audit
record and the account's own session list have to show.

Disabling an account and revoking its sessions are deliberately separate. One
is reversible; the other logs the person out of every device. A disabled
account stops authenticating on its sessions' next request, and that refusal
does not slide the idle window on the way past.

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

## Not yet here

CSRF, login, and enrollment are the tasks that follow. This page describes the
session store, its lifecycle, and its cookie policy; nothing in it is an
authentication boundary on its own yet.

## Related

- [Password hashing](passwords.md)
- [Security model](security.md)
- [Database queries and sqlc](database-queries.md)
- [Runtime packages](runtime-packages.md)
