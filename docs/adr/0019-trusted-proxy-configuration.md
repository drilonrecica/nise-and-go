# 0019: Trust a declared proxy chain, counted from the right

- **Status:** Accepted
- **Date:** 2026-08-31
- **Amends:** [ADR 0011](0011-runtime-public-api.md), [ADR 0017](0017-security-primitives-in-runtime.md)
- **Closes:** the `THREAT_MODEL` open question on trusted-proxy configuration and header normalization (M5-014)

## Context

Behind a reverse proxy, the connection's own peer address is the proxy, not the
client. Every control that cares who is asking — rate limiting, audit records,
throttling, an eventual geo-restriction — needs the real one, and the only place
it exists is a header the client can also write.

`X-Forwarded-For` is a list, and the near-universal mistake is to read its
leftmost entry. That entry is whatever the client sent. A request arriving with
`X-Forwarded-For: 10.0.0.1` came from whoever sent it, and reading the left of
that list hands an attacker complete control of the address every one of those
controls will use — including the address a rate limiter buckets by, which turns
a defense into a way to lock other people out.

The list is only meaningful from the right. Each proxy appends the address it
received the request from, so the rightmost entry was written by the proxy
closest to this server, the one before it by the proxy before that, and so on
until the entries stop being ones this deployment produced. Where that boundary
falls is a fact about topology. No library can know it.

There is a second question, and conflating it with the first is how deployments
get this wrong: *how many* proxies are in front, and *who* is allowed to be one.
A hop count is exact when the chain is fixed, which it is for the deployment
targets this project documents. It is not enough on its own if the application's
port becomes reachable without going through the proxy at all — a misconfigured
firewall, a container published to the host, a service mesh sidecar removed.

## Decision

### `runtime/forwarded` owns the resolution

A new runtime package, admitted under ADR 0017's criterion: an application that
gets this wrong has a vulnerability, not a preference. It is small, it imports
only the standard library, and it answers one question.

### Two settings, both defaulting to trusting nothing

| Setting | Default | Meaning |
|---|---:|---|
| `TRUSTED_PROXY_COUNT` | `0` | How many reverse proxies are in front of this process. |
| `TRUSTED_PROXY_NETWORKS` | *(empty)* | Which peers may be one, as CIDR blocks or bare addresses. |

`TRUSTED_PROXY_COUNT=0` means forwarded headers are **not read at all**, whatever
they contain, and the connection's own peer is the client. That is correct for a
service exposed directly, and it is what an application that never thinks about
this gets.

With a count of N, the client address is the entry N from the right. A forged
prefix is then harmless by construction: it sits to the left of the count and is
never read. This is why the count, not a "take the first public address"
heuristic, is the rule.

`TRUSTED_PROXY_NETWORKS` is the second gate. When it is set, a request whose
immediate peer is outside it has its forwarded headers ignored entirely, so the
day the application's port becomes directly reachable is not the day an attacker
can name their own address.

### Falling back, not repairing

A trusted request whose header is shorter than the declared count, or whose
selected entry does not parse, falls back to the connection's peer and reports
itself untrusted. It is a misconfiguration, and inventing an address from the
entries that happen to be present would be guessing at exactly the moment the
deployment's assumptions are known to be wrong.

An unparseable entry is refused rather than skipped, because skipping would let
a client shift the count by inserting garbage.

### `X-Forwarded-Proto` and `X-Forwarded-Host` use the same index

Both are validated against a closed shape — `http` or `https`, and a host
containing only characters a host may contain. A value outside it leaves the
request's own scheme or host in place, so a forwarded header cannot smuggle a
path or a query into anything that later builds a URL.

### `TRUST_PROXY_HEADERS` must agree

The existing `TRUST_PROXY_HEADERS`, which governs adopting inbound request and
correlation IDs, is now refused when `TRUSTED_PROXY_COUNT` is zero. Trusting a
proxy's identifiers while declaring that there is no proxy is a half-configured
state, and the two settings answering differently is precisely the kind of
inconsistency that survives review.

### What is excluded

- **RFC 7239 `Forwarded`.** Supporting a second mechanism for the same fact
  doubles what a proxy has to strip correctly and what this code has to agree
  with itself about. Every proxy this project documents writes `X-Forwarded-*`.
- **Heuristics.** No "first non-private address", no "trust anything from a
  private range in the list". Both are guesses that an attacker can aim.
- **Automatic cloud-provider ranges.** Fetching a provider's published ranges
  would be a network dependency, and a stale copy would be worse than a count.

## Consequences

- A deployment behind Coolify or one Compose proxy sets `TRUSTED_PROXY_COUNT=1`
  and is done. A deployment that sets nothing is safe and slightly wrong about
  addresses, which is the right way round.
- An operator who adds a CDN in front of an existing proxy must raise the count.
  Forgetting to is visible: the resolved address becomes the CDN's, and requests
  report themselves untrusted only if the chain is *shorter* than declared, not
  longer. This is the sharpest edge of the count model and is documented as such.
- `runtime/` reaches twelve packages.
- Revisit if a deployment target appears whose proxy chain length genuinely
  varies per request; the count model cannot express that, and the honest
  response would be a different mechanism rather than a heuristic bolted onto
  this one.
