# 0020: Refuse common passwords locally; never ship a remote checker

- **Status:** Accepted
- **Date:** 2026-08-31
- **Closes:** the `THREAT_MODEL` open question on the compromised-password data source and its privacy model (M5-017)

## Context

The product's password policy is "sufficient length, reject known-compromised,
no arbitrary composition rules". The second clause needs a source, and every
available source costs something.

**Have I Been Pwned's Pwned Passwords API** is the obvious answer and is
genuinely well designed: a client sends the first five hex characters of the
password's SHA-1 hash and receives every suffix under that prefix, comparing
locally. Nothing leaves the process that identifies the password.

It is still an outbound network call from the application, on the login and
password-change paths, to a third party. This project has a standing rule
against exactly that: no remote service dependency, no implicit network traffic,
and a `test/nonetwork` suite that enforces it. Making the default password
policy depend on a third party's availability would also mean deciding, on every
owner's behalf, that their users' password-setting events may be timed by
somebody else's logs.

**A local breach corpus** avoids the network and costs disk instead: the full
Pwned Passwords set is tens of gigabytes raw, and a probabilistic filter over it
is still a large artifact to ship, version, and update. Nothing about generating
a project should involve a multi-gigabyte download.

**Nothing at all** is the honest description of what a length rule alone
achieves against credential stuffing, which is the attack this clause exists
for.

## Decision

### Ship a small local list, and be precise about what it is

`runtime/password` embeds a few hundred of the passwords that appear at the top
of every credential-stuffing list, and refuses them. It is checked in memory,
folds case and surrounding whitespace, and makes no network call.

It is **not** a breach corpus and the documentation never calls it one. It stops
`password`, `qwerty`, `changeme`, and their neighbours — which is most of what
an unthrottled stuffing run actually tries — and stops nothing else.

Folding is limited to case and whitespace. Stripping digits or punctuation so
that `p@ssw0rd!` matched `password` would be a much stronger claim than this list
can support and would refuse passwords that are genuinely fine.

### The checker is an interface, and Nise ships no remote implementation

`password.Compromised` has one method. An owner who wants the full corpus writes
the adapter — HIBP's k-anonymity API, a local filter, an internal service — and
in doing so makes the privacy decision themselves, with the trade-off in front
of them rather than inherited from a framework default.

Nise will not ship that adapter, because shipping it is what makes it the path
of least resistance.

### An unavailable checker denies, it does not allow

`IsCompromised` returning an error is not the same as returning "no". A caller
must treat them differently: failing open on an unavailable checker turns an
outage into an accepted weak password, silently, exactly when nobody is looking.

### The privacy model, stated for each choice

- **Default (built-in list):** nothing leaves the process. The comparison is a
  map lookup on a case-folded string.
- **A local corpus an owner supplies:** nothing leaves the process. The cost is
  the artifact.
- **A remote k-anonymity API an owner adds:** a 20-bit prefix of the password's
  SHA-1 leaves the process, along with the fact that a password was being set or
  checked at that moment, by an IP the third party can see. Twenty bits narrows
  the candidate space by about a millionth; it does not identify the password,
  and it is not nothing. An owner adding this should say so in their own privacy
  documentation, which is a sentence Nise cannot write for them.

## Consequences

- A generated application refuses the worst passwords out of the box, with no
  configuration, no network, and no artifact to download.
- It does **not** refuse a password that appears once in a 2019 breach and
  nowhere else. Anybody who needs that behavior has a documented seam and a
  documented cost; anybody who does not is not silently sending prefixes of
  their users' passwords anywhere.
- The built-in list needs occasional review as stuffing lists shift. It is a
  plain text file with one entry per line, so that review is a readable diff.
- Revisit if a compact, offline, freely redistributable breach dataset appears
  that is small enough to embed honestly. The decision here is about the cost of
  each option, not about the goal.
