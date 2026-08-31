# Pagination

Cursor pagination is the default for operational lists. Offset pagination is a
separate contract reserved for reports where a page number is meaningful; it is
not a mode of this one, and the two never share a page schema.

## The client contract

A cursor-paginated collection accepts three query parameters and returns a
`CursorPage`:

| Parameter | Meaning |
|---|---|
| `limit` | Page size. Absent takes the collection's default. |
| `after` | Read the page following a cursor the collection issued as `next_cursor`. |
| `before` | Read the page preceding a cursor it issued as `prev_cursor`. |

```json
{
  "items": [],
  "page": { "has_more": true, "next_cursor": "nc1.y2026a.eyJk…", "prev_cursor": "nc1.y2026a.eyJk…" }
}
```

`has_more` reports whether another page exists in the direction this page was
read. A cursor member is **absent**, never `null` and never `""`, when there is
no page that way, so a client tests for presence and nothing else.

A cursor is opaque. Return it verbatim; do not parse it, construct one, store
one beyond the current listing, or move one between users. It is a position in
one specific query, not an identifier.

## Refusals

Every pagination failure is the client's, so all of them are `400` with an RFC
9457 Problem body. There are two codes:

| Code | When |
|---|---|
| `cursor_expired` | The cursor verified but is past `CURSOR_TTL`. Start the listing again. |
| `report_too_deep` | The report page starts past the offset ceiling. Narrow the filters. |
| `invalid_pagination` | Everything else. |

The generic code deliberately covers a forged cursor, a cursor from another
query, a cursor used in the wrong direction, an unparseable token, an
unsupported token version, an out-of-range `limit`, and `after` combined with
`before`. Which invariant a probe broke is in the server's logs, not in its
response.

Specific refusals worth knowing about as a client author:

- **A changed filter invalidates outstanding cursors.** The cursor is bound to
  the query it was issued for. Changing a filter, sort, or search parameter and
  keeping the cursor is refused rather than answered wrongly.
- **`limit` is not clamped.** `?limit=5000` is an error, not a large page.
- **A parameter present with an empty value is an error.** `?limit=` is a URL
  built wrongly, not a request for the default.
- **A repeated parameter is an error.** `?limit=5&limit=10` is two
  contradictory instructions.

## The token

```
nc1.<key-id>.<base64url payload>.<base64url HMAC-SHA256 tag>
```

The authenticated payload carries the direction, the expiry, the sort-key
values, and a fingerprint of the query. [ADR 0016](adr/0016-authenticated-cursor-pagination.md)
explains why each of those is there and what was excluded.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `CURSOR_SIGNING_KEY` | *(generated)* | Active signing key, `<id>:<base64 secret>`. Secret is at least 32 bytes. |
| `CURSOR_RETIRED_KEYS` | *(none)* | Comma-separated verification-only keys, same form. |
| `CURSOR_TTL` | `1h` | How long an issued cursor stays valid. At most `24h`. |

Generate a key with:

```sh
echo "y2026a:$(openssl rand -base64 32)"
```

The identifier is public — it travels in every cursor so a decoder can select
the verification key — and is what makes a rotation visible in a log.

Leaving `CURSOR_SIGNING_KEY` unset generates a random key for the process. That
is fine on a development machine and **refused in production**: every restart
and every replica would reject cursors the others issued. There is no built-in
default key, because a default signing key is one that everybody who has read
this repository holds.

### Rotating

Rotation is two deployments and no broken cursors in between:

1. Move the current `CURSOR_SIGNING_KEY` value into `CURSOR_RETIRED_KEYS` and
   set a new `CURSOR_SIGNING_KEY`. New cursors are signed with the new key;
   outstanding ones still verify.
2. After `CURSOR_TTL` has elapsed, remove the retired entry.

A ring holds at most eight retired keys, and every identifier in it must be
distinct.

## Implementing a collection

`runtime/pagination` owns the token, the key ring, and the parsing.
`internal/platform/httpapi` owns the decisions:

```go
limits, err := pagination.NewLimits(25, 100)      // this collection's policy
binding := cursorBinding(r, "/invoices")          // everything except limit/after/before
page, err := s.cursors.ParsePage(binding, r.URL.Query(), limits)
if err != nil {
        problem.Handler(paginationProblem(err))(w, r, err)
        return
}
// … run the query using page.Limit, page.Direction, page.Cursor.Values …
meta, err := issueCursorPage(s.cursors, binding, pageBoundaries{
        First: firstRowKey, Last: lastRowKey, HasMore: more, HasPrevious: previous,
})
```

Four rules the generated helpers exist to keep:

- **Query one row more than `page.Limit`.** That extra row is how `has_more` is
  known; return it in the metadata, not in `items`.
- **The sort key must be unique.** A cursor on a non-unique column skips or
  repeats rows at a page boundary. Append the primary key.
- **Every filter belongs in the binding.** `cursorBinding` excludes exactly
  `limit`, `after`, and `before`. A filter that does not travel in the query
  string — a path parameter, or the caller's tenant — goes into the resource
  argument, so two different result sets can never share one binding.
- **Normalize nil items.** A Go slice is nil-able; the contract says `items` is
  an array. The collection constructor is where that is enforced.

## Offset reporting pagination

A report is a different question from a listing: a page number is something a
person quotes, and a total is something they need. So it is a separate
contract, not a mode of the cursor one. The parameters are different, the page
schema is different, and a request that mixes the two is refused.

| Parameter | Meaning |
|---|---|
| `page` | 1-based page number. Absent means the first page. |
| `size` | Page size. Absent takes the report's default. |

```json
{
  "items": [],
  "page": { "page": 2, "size": 25, "total": 51, "total_pages": 3, "has_more": true }
}
```

`total_pages` is `0` for an empty result set, so "page 1 of 0" reads as an
empty report rather than as a page that does not exist. `page` and `size` are
echoed from the request the totals were computed for, never recomputed, so a
report can never describe a page other than the one it returned.

### Bounds

- `page` is 1 through 100,000, and `size` is 1 through 200. Neither is clamped.
- The **offset ceiling** is the real limit. A report configures its own with
  `NewReportLimits(defaultSize, maxSize, maxOffset)`, up to 1,000,000. A page
  whose first row is past it is refused with `report_too_deep`, because past
  that point the database discards more rows than it returns and the honest
  answer is a narrower filter or an export, not a slower page.
- `Report.Offset()` returns `int64`. Two in-range `int` values do not have an
  in-range `int` product on a 32-bit platform, and this value goes straight
  into SQL.
- `Report.Totals(total)` re-checks the report's bounds, which is what lets the
  transport layer narrow `page` and `size` to the contract's `int32` without a
  silent wrap.

### Choosing between the two

Use cursor pagination for anything a user scrolls: it is stable under
concurrent inserts and it does not get slower on page 400. Use a report when
the page number is part of the answer — an exported statement, a printed
listing, a table where "page 7 of 12" is the point. A report pays for that with
a `COUNT`, with rows that shift under it as data changes, and with a depth
ceiling.

## Related

- [OpenAPI server bindings and frontend client](openapi.md)
- [Runtime packages](runtime-packages.md)
- [Configuration](configuration.md)
- [ADR 0016](adr/0016-authenticated-cursor-pagination.md)
