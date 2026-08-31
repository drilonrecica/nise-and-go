# Password Hashing

Generated applications hash passwords with Argon2id through `runtime/password`.
There is one algorithm and no pluggable interface: an application that must
move off Argon2id writes that migration explicitly, rather than selecting the
wrong algorithm as easily as the right one
([ADR 0017](adr/0017-security-primitives-in-runtime.md)).

## Versioned parameters

A password hash outlives every parameter choice. Hardware gets faster, and the
cost that hurt an attacker in 2026 will not hurt one in 2031, so a deployment
has to raise its parameters without invalidating stored hashes.

A **policy** holds one current parameter set plus the superseded sets it still
accepts:

- Verifying a hash written under a superseded set succeeds and reports
  `NeedsRehash`. That is the caller's signal to rewrite the record — at login,
  the one moment the server legitimately holds the plaintext.
- Verifying a hash whose cost is not in the policy at all **fails**. Removing a
  set from the list is the explicit, auditable way to retire it, and it locks
  those accounts out of password login until they reset.

Hashes are stored in the standard PHC string format:

```
$argon2id$v=19$m=47104,t=3,p=1$<base64 salt>$<base64 tag>
```

The parameters travel with the hash, so verification never guesses which set
produced a record, and a deployment that leaves Nise can read its own password
column with any Argon2 library.

## The parameter history is code, not configuration

`internal/platform/passwords` holds the history in Go. Two things depend on
every replica agreeing: which stored hashes verify, and which get rewritten. A
replica reading a different environment variable would silently lock some
accounts out and silently leave others on old parameters, and neither failure
announces itself.

Raising parameters is therefore a reviewed code change:

1. Run the benchmark on the hardware that will serve logins.
2. Add the recommendation as a new version and make it current.
3. Move the previous set into the superseded list.
4. Remove it only once its records have been rehashed at login.

## Benchmarking

Parameters are a property of the hardware that runs them, so the shipped
default is a **floor** meeting current OWASP guidance — not a claim about
anyone's server.

```sh
myapp password benchmark
myapp password benchmark --memory 65536 --target 500ms --samples 5 --json
```

| Flag | Default | Meaning |
|---|---:|---|
| `--memory` | `19456` (19 MiB) | Memory cost in KiB, held fixed while passes are searched. |
| `--parallelism` | `1` | Degree of parallelism, held fixed. |
| `--target` | `250ms` | Per-login budget the recommendation must stay inside. |
| `--samples` | `3` | Evaluations per measurement; the median is reported. |

The search holds memory fixed and raises passes, because memory is the cost an
attacker with parallel hardware finds hardest to buy: pick the largest memory a
login can afford, then let the benchmark find the passes that fit. When the
report says the search stopped at the iteration ceiling rather than at the
target, the machine has room to spare — raise `--memory`, not passes.

The command reads no configuration and opens no database. That is deliberate:
the answer depends on the host's CPU and memory and on nothing else, so it is
safe to run on a production machine without production credentials.

## Timing and the unknown-account path

`Policy.VerifyDummy` runs the same Argon2 work against a throwaway hash
generated per process. Call it on the login path where no account exists.
Skipping the hash there turns response time into an account-existence oracle,
and identical response bodies do not fix that.

The dummy record is generated at policy construction, so two deployments never
share one, and the cost is paid once rather than per request.

## Bounds

| Bound | Value |
|---|---:|
| Minimum memory | 19 MiB (OWASP floor) |
| Maximum memory | 4 GiB |
| Iterations | 1–64 |
| Parallelism | 1–64 |
| Salt | 16 bytes, random per record |
| Tag | 32 bytes |
| Password length | 1–1024 bytes |

The maximums exist so a mistyped parameter is a startup failure rather than a
server that allocates terabytes per login attempt. The password bound is not
about work — Argon2's cost does not grow with input length — but about refusing
an unbounded value at the boundary.

## Related

- [Runtime packages](runtime-packages.md)
- [Security model](security.md)
- [ADR 0017](adr/0017-security-primitives-in-runtime.md)
