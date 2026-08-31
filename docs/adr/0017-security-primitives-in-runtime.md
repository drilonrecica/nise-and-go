# 0017: Security primitives belong to the runtime, security policy to the application

- **Status:** Accepted
- **Date:** 2026-08-31
- **Amends:** [ADR 0011](0011-runtime-public-api.md)
- **Amended by:** [ADR 0019](0019-trusted-proxy-configuration.md), which admits `runtime/forwarded` under the same criterion

## Context

Milestone 5 adds the security boundary: password hashing, opaque sessions,
CSRF, permissions, and default-deny authorization. Every one of those has to
land somewhere, and ADR 0011 requires an ADR before any package joins
`runtime/`. Deciding them one at a time would produce four thin records that
each re-argue the same question, and would risk four inconsistent answers.

The question is one question. Milestones 3 and 4 already answered it twice, in
the same direction:

- The strict JSON decoder, the Problem catalog, and the collection constructors
  are application-owned templates, because an application that changes its body
  limit or adds a problem type has expressed a preference.
- The transaction runner ([ADR 0015](0015-use-case-owned-transactions.md)) and
  the cursor codec ([ADR 0016](0016-authenticated-cursor-pagination.md)) are
  runtime packages, because an application that changes them has introduced a
  bug it is unlikely to notice.

The dividing line that produced both answers: **an application that weakens a
policy has made a decision; an application that weakens a primitive has a
vulnerability.** Nobody reviews the Argon2 encoding in their own repository
before shipping, and nobody should have to.

There is a second pressure specific to this milestone. Nise cannot claim
"maximum security" and it does not intend to. What it can do is make the small
number of things that are genuinely easy to get catastrophically wrong —
constant-time comparison, token entropy, parameter migration, default-deny —
into code that is written once, tested adversarially, and versioned.

## Decision

### Three packages join the runtime surface

| Package | Owns | Task |
|---|---|---|
| `runtime/password` | Argon2id hashing, versioned parameter sets, rehash detection, constant-cost dummy verification, and parameter benchmarking | M5-004 |
| `runtime/session` | Opaque session token generation, hashing, and the driver-independent session lifecycle value types | M5-001 |
| `runtime/authz` | Permissions, role bundles, and the default-deny decision primitive | M5-007 |

No fourth package is added for CSRF. A CSRF token is bound to a session, so the
token itself belongs to `runtime/session`; the middleware that checks it,
together with the Origin and Fetch Metadata rules and the list of acceptable
origins, is application configuration and lives in the generated
`internal/platform`.

### What stays with the application

Everything that is a choice, and everything that touches the application's own
schema:

- The password parameter history, including which superseded sets are still
  accepted. It is Go code, not configuration: every replica must agree, and the
  list has to match what is actually stored in the database. Two replicas
  disagreeing about which hashes verify is not a tuning difference.
- Password composition and minimum length, and whether a compromised-password
  check is consulted.
- Session, user, role, and audit tables and their migrations. Nise never owns a
  table in an application's schema.
- Session lifetime, cookie names beyond the required prefix, the origins that
  may make state-changing requests, throttling thresholds, and which actions
  demand reauthentication.
- Every authorization decision. `runtime/authz` supplies the primitive; the
  permission names, the role bundles, and the use cases that ask are the
  application's.

### First-run defaults are floors, not recommendations

`password.Default()` meets current OWASP guidance and nothing more. Parameters
are a property of the hardware that runs them, so the shipped value is a floor
a deployment is expected to raise using the benchmark on its own machine — not
a number Nise claims is right for anyone's server.

### What is excluded

- No JWTs, anywhere ([ADR 0006](0006-opaque-sessions-no-jwt.md)).
- No `runtime/auth` or `runtime/security` package. Both names attract anything
  security-shaped and would become the god object ADR 0011 rejected under other
  names.
- No middleware aggregator. The rule from ADR 0011 holds: middleware lives with
  the concern it implements, and the ordered chain is generated application
  code.
- No pluggable hashing interface. One algorithm, versioned parameters. An
  application that must migrate off Argon2id writes that migration explicitly;
  an interface would only make the wrong algorithm as easy to select as the
  right one.

## Consequences

- `runtime/` reaches eleven packages. That is a real commitment, and ADR 0011's
  standing instruction to revisit the list after the reference application now
  has more to weigh.
- The password parameter history being code means raising parameters is a
  deployment, not a configuration change. That is the intended cost: it is
  reviewed, it is in version control, and it cannot differ between replicas.
- An application can still do the wrong thing — it owns its use cases, and
  `runtime/authz` cannot force it to ask. Default-deny reduces that to a
  visible omission rather than a silent allow, which is the most a library can
  honestly offer.
- Revisit if a second profile needs a different session or password model, or
  if the reference application shows one of these three packages is carrying
  something that was really a policy all along.
