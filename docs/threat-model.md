# Threat model

This is the framework's threat model: what a generated application defends
against **before** it has any features of its own, what it does not, and where
the boundary between the two sits.

**Every application needs its own.** This one covers the parts Nise wrote. It
knows nothing about your domain, your data's sensitivity, who your users are,
or what an attacker would want from you — and those are the questions that
decide most of what matters. Read this as the floor, not the answer.

## What is being protected

| Asset | Why it is worth taking |
|---|---|
| Session identifiers | A stolen one is the user, without their password |
| Password hashes | Offline cracking, and password reuse across sites |
| Authorization and tenant boundaries | The difference between one customer's data and everyone's |
| Business and personal data in PostgreSQL | Usually the actual target |
| Uploaded files and presigned upload capability | Both the content and the ability to write objects |
| Email and notification content | Frequently carries single-use links |
| Audit records | Their integrity is what makes an incident reconstructable |
| Migrations and backups | A backup is a complete copy with none of the access control |
| Release artifacts and generator templates | Compromise here reaches every downstream application |
| Deployment secrets | Everything above, at once |

## Who is being defended against

- An unauthenticated attacker on the internet.
- An authenticated user reaching past what they were granted.
- A user in one organization reaching another's data.
- Someone holding a leaked session or device token.
- Someone uploading a file chosen to cause harm.
- Automated credential stuffing and resource exhaustion.
- A compromised dependency or release workflow.
- A developer mistake in generated or customized authorization code.

The last one is not a courtesy inclusion. Most authorization failures in real
systems are somebody forgetting a check, not somebody defeating one, and
several controls here exist specifically because a person will forget.

## The boundaries, and what happens at each

### Browser to HTTP edge

An attacker controls every byte of the request.

Refused: a body over the limit, a body that is not the declared media type,
unknown JSON fields, duplicate JSON keys, more than one JSON value, a
cross-site state-changing request (Origin, Fetch Metadata, and an anti-forgery
token), and a session cookie without the `__Host-` prefix in production.

Rate-limited: authentication, per address and per client, counted **before**
password hashing — so a flood costs the attacker a request and costs the
server a counter increment rather than an Argon2id computation.

Generic: a failed sign-in returns one answer for "no such account", "wrong
password", and "account disabled". The distinguishing outcome reaches the
audit record and nothing else.

### Application to PostgreSQL

Queries are parameterized by sqlc; the tenant identifier is a bound
parameter, not interpolation.

With the organizations module, row-level security is `FORCE`, so the table
owner is not exempt, and the application **refuses to start in production**
when its role is a superuser or carries `BYPASSRLS` — because either bypasses
every policy, and a schema that looks complete while enforcing nothing is the
worst outcome available.

Tenant context is `SET LOCAL`, reverted by the transaction's end whatever
ends it, so a pooled connection cannot carry one request's tenant into the
next.

A transaction with no tenant established reads **nothing**. That direction is
chosen: a forgotten context produces an empty page somebody reports, and the
other direction produces another tenant's data that nobody notices.

### Web process to background job

A job has no request, so nothing establishes its tenant unless the job does.
A worker that forgets reads nothing, for the same reason and by the same
mechanism.

Job payloads are visible to anyone who can read the database. They should
carry identifiers, not secrets — a single-use token in a job argument is a
token stored in a queue.

A job is retried, so **every job must be safe to run twice**. There is no
exactly-once delivery, here or anywhere.

### Application to SMTP and object storage

Both are third parties that receive whatever they are given. That is not a
vulnerability, it is what they are for, and it is a data-sharing decision the
application makes.

Refused before anything is sent: any `\r`, `\n`, or NUL in a mail header
value, which is how a form value becomes a `Bcc` or a second message. Refused
at startup: SMTP credentials without encryption, and a server that does not
offer STARTTLS when STARTTLS was configured.

Object keys are derived by the application from randomness it generates, never
from anything a client sent. Presigned URLs are bearer capabilities with a
one-hour maximum, because a signature already handed out cannot learn that an
account was disabled.

### Uploads

Everything a client says about an upload is a claim: the content type is
determined from the stored bytes, the size is measured after the fact, and the
filename is display metadata that never becomes part of a key.

An object stays in a quarantine prefix until it has passed every check, and
moves out in one operation — so a refused object has never existed at a
reachable key.

**No malware scanning happens unless the application configures a scanner**,
and a generated project configures none. See
[ADR 0027](adr/0027-upload-malware-scanning-boundary.md).

### Developer workstation to release

Dependencies are pinned and checksum-verified. Tools are installed at pinned
versions through the module proxy. Vulnerability and secret scanning run on
every push and weekly, because a vulnerability database changes without the
repository changing.

Nise itself performs **no** implicit network access: no telemetry, no update
check, no remote password lookup, no signature feed. Three separate test
suites prove it, one of them by running commands inside a network namespace
with no route out.

### Backups and restore

A backup is a complete copy of the data with none of the application's access
control. Its custody is the operator's problem, and it is a larger one than
the application's own.

The generated `db backup` command encrypts every backup with AES-256-GCM under
the STREAM construction, so a backup that has been modified, reordered,
truncated, appended to, or whose header was edited fails to open rather than
restoring into a database missing part of itself. `db verify` restores a
backup into a scratch database rather than checking a checksum. See
[Backups](backups.md).

What that leaves: the key. `BACKUP_ENCRYPTION_KEY` has no rotation procedure
and no re-encryption command, so an archive is only as separable from a leaked
key as the operator's own key management makes it. That is stated here as an
unsolved problem rather than implied away, and it is listed among the
[residual risks](security.md#residual-risks).

## What is deliberately out of scope

Naming these is the point of the section. Each is a real risk that this
framework does not address, so that nobody assumes otherwise.

- **Denial of service by volume.** Throttling protects authentication and
  bounds request bodies. It is not a defence against a botnet, and nothing
  here substitutes for infrastructure that is.
- **A compromised host.** An attacker with code execution on the application
  server has the database credential, the session table, and the signing keys.
  Nothing in a single-binary application changes that.
- **A malicious operator.** The person who can read the environment can read
  everything.
- **Client-side compromise.** A user's browser extension, malware, or a
  compromised device is outside what a server can reach.
- **Physical and cloud-provider trust.** The disk, the hypervisor, and the
  object store are trusted.
- **Traffic analysis.** Sizes and timings are not padded.
- **Malware in uploads**, unless a scanner is configured.

## Residual risks

See [security](security.md#residual-risks), which is the version kept current
alongside the code.

## Extending this

A generated application's own threat model should start by answering the
questions this one cannot:

1. What is the most damaging thing an attacker could do with your data,
   specifically?
2. Which of your endpoints changes something that cannot be undone?
3. Who, in your organization, can already do that legitimately — and what
   would you see if their account were used to do it at 3am?
4. Which third parties receive your users' data, and did your users agree?
5. What is your restore procedure, and when did you last run it?

The controls above are worth exactly as much as the answers to those.
