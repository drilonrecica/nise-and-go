# 0016: Authenticate pagination cursors and bind them to their query

- **Status:** Accepted
- **Date:** 2026-08-31
- **Amends:** [ADR 0011](0011-runtime-public-api.md)

## Context

Cursor pagination is the default for operational lists in this project's
locked decisions, and offset pagination is reserved for reports where a page
number is meaningful. A cursor is therefore going to appear in every generated
list endpoint, and its format is a public contract the moment the first client
stores one.

The naive form of a cursor is the last row's sort key in the clear —
`?after=41812`, or a base64 wrapper around the same value, which is encoding,
not protection. Three things then go wrong, and none of them announces itself:

- **Position forgery.** A client edits the value and reads from a position the
  query never offered it. With a compound sort key, or a key derived from
  another user's row, this is a read of data the endpoint intended to page
  past, not to expose.
- **Query drift.** A cursor cut from `?status=open` is replayed against
  `?status=closed`. The comparison still evaluates, so the server returns a
  page that is internally consistent and semantically wrong. Nothing errors.
- **Unbounded lifetime.** A cursor kept for a month resumes a listing whose
  ordering no longer resembles the one the cursor was cut from.

Milestone 4 also has to decide where the format lives. The strict decoder
(`httpjson`) and the Problem catalog (`problem`) are application-owned
templates because they encode *policy* an application should be able to change.
A cursor's authentication is not policy: an application that weakens it has a
vulnerability, not a preference.

## Decision

### The token is authenticated, versioned, and bound

A cursor is

```
nc1.<key-id>.<base64url payload>.<base64url HMAC-SHA256 tag>
```

The version prefix and key identifier sit outside the payload because both are
needed before the payload can be trusted: the version selects the parser, the
identifier selects the verification key. The tag covers all three fields,
length-prefixed, so no byte can be moved across a field boundary without
invalidating it.

The payload carries the direction, the expiry, the sort-key values, and the
**fingerprint of the query the cursor was issued for**. The fingerprint is
inside the authenticated payload rather than only in the tag's input, which is
what makes "this cursor belongs to a different query" a distinct, diagnosable
refusal instead of an indistinguishable signature failure.

A cursor is bound to a direction. A cursor issued as `next_cursor` is accepted
only in `after`, and one issued as `prev_cursor` only in `before`. Reading the
wrong side of a position is a silent wrong answer otherwise.

### The codec is a runtime package

`runtime/pagination` is added to the surface ADR 0011 fixes. It owns the token
format, the key ring, the closed direction set, and the parsing of `limit`,
`after`, and `before`. It imports the standard library only.

The generated application still owns everything that is a decision: which
collections paginate, their default and maximum page size, how their sort key
maps onto cursor values, which request parameters count as filters, and which
Problem the failure becomes. `internal/platform/httpapi` holds that wiring.

### Rotation is a two-step deployment

A key ring has one active key, which signs, and up to eight retired keys, which
only verify. Rotating means promoting a new active key while keeping the
previous one retired, then dropping the retired key once `CURSOR_TTL` has
elapsed. There is no window in which outstanding cursors break.

### An unset key generates an ephemeral one, and production refuses it

`CURSOR_SIGNING_KEY` unset generates a random key for the process. A checked-in
default would be a signing key held by everyone who has ever read the
repository, so there is no default. Production fails to start on the ephemeral
path, because every restart and every replica would reject cursors the others
issued.

### What is excluded

- Encrypting the payload. The tag stops forgery; the payload holds a sort key
  the client is already paging through, not a secret. Encryption would add a
  second failure mode and hide nothing an authorized client cannot already see.
- Storing issued cursors server-side. That turns a stateless read into a write
  and a cleanup job, for a threat the tag already covers.
- Making a cursor a page *number*. Offset pagination is a separate contract
  (`ReportPage`), not a mode of this one.
- Clamping an out-of-range `limit`. A silently resized page hides a client bug.

## Consequences

- Every generated list endpoint gets tamper resistance, expiry, and query
  binding without each one re-deriving them, and the failure modes are testable
  in one place.
- `runtime/` grows to eight packages. ADR 0011 asked for a revisit of the list
  after the reference application; this is an addition ahead of that review,
  justified by the defect class rather than by convenience.
- A deployment now has a required production secret it did not have before, and
  an operator who rotates it carelessly invalidates outstanding cursors. The
  retired-key list exists precisely so that careless is not the only option.
- Cursors do not survive a `CURSOR_TTL` gap, by construction. A client that
  parks a listing overnight starts it again; this is the intended behavior, not
  a defect to work around by raising the ceiling.
- Revisit if a profile appears whose lists are not orderable by a stable key,
  or if a real application shows the eight-value sort-key bound is too small.
