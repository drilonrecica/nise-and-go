# Backups

A generated application takes, verifies, and restores its own backups:

```sh
./myapp db backup  -out /backups/myapp-2026-09-01.backup
./myapp db verify  -from /backups/myapp-2026-09-01.backup
./myapp db restore -from /backups/myapp-2026-09-01.backup -confirm myapp
```

They are commands of the **application binary**, not of `nise`.
[`nise db`](commands/db.md) builds the application from source before running
it, which is right for `migrate` and `status` on a developer machine and wrong
for a production backup. The binary already in the deployment is the one that
should be dumping the database beside it.

## What they are, and are not

`pg_dump` and `pg_restore` do the dump and the restore. This application
orchestrates them, encrypts what comes out, and refuses when something is
wrong.

That is deliberate. A logical dump has to get extensions, sequences,
ownership, large objects, and dependency ordering right, and a hand-written
approximation is a backup that restores into a database subtly unlike the one
it came from — which is worse than no backup, because somebody is relying on
it.

So both tools are **required on `PATH`**, and their absence is an error naming
the package to install rather than a silent fallback:

```
backup: the PostgreSQL client tools are not available: pg_dump is not on PATH.
Backups run from a machine with the PostgreSQL client tools; this application's
own image deliberately carries none.
Install postgresql-client (Debian/Ubuntu) or postgresql (Fedora/Homebrew).
```

They are genuinely not in the [runtime image](deployment.md): it has no shell
and no client tools, which is the property that makes it worth shipping. Run
backups from an operator machine, a scheduled job, or a sidecar that has them.

`pg_dump` must be at least the server's version. An older `pg_dump` silently
omits features the newer server has, and that is discovered at restore time.

## Verification means restoring

`db verify` restores the backup into a scratch database, checks the schema
arrives at the migration version the backup records, and drops the scratch
database — including when the restore fails.

It is not a checksum. A checksum proves the bytes are the bytes; it says
nothing about whether `pg_restore` will accept them, or whether what comes out
is a schema this application understands. **A backup nobody has restored is a
hypothesis.**

The scratch database is named `nise_verify_<random>` and is created from
`template0` on the same server, so it inherits nothing anybody installed into
`template1`. Verification therefore needs a role that can `CREATEDB`.

Run it on a schedule, not once. A backup pipeline that worked in March and
broke in April looks identical from the outside until the day it is needed.

## Encryption

Every backup is encrypted with AES-256-GCM under the
[STREAM construction](https://eprint.iacr.org/2015/189): the file is a
sequence of 1 MiB chunks, each sealed under a nonce built from a per-file
random prefix, the chunk's index, and a flag marking the last one.

| Attack | What the construction does |
|---|---|
| Modify a chunk | The tag fails |
| Reorder or duplicate chunks | The counter is in the nonce, so it fails |
| **Truncate the file** | The last chunk read is not marked final, so it fails |
| Append to the file | Data after the final chunk is refused |
| Edit the header | The header is the additional authenticated data; the body stops opening |

The truncation row is the one that motivates the whole design. A plain
per-chunk AEAD authenticates each chunk and nothing about the sequence, so a
file cut in half decrypts happily into a database missing its second half and
says nothing about it.

Nothing is ever buffered: the dump is encrypted as it streams out of
`pg_dump`, and decrypted as it streams into `pg_restore`. A backup of any size
costs one chunk of memory, and **no plaintext copy of the database is ever
written to disk** — a verification that staged one would leave the thing the
encryption is for sitting in a temporary directory, and on the day it is
interrupted it would leave it there.

A wrong key and a tampered file report the same error. Distinguishing them
tells somebody probing a backup which of the two they got wrong, and the
caller can do nothing different with the answer.

### The key

`BACKUP_ENCRYPTION_KEY` is base64 of exactly 32 random bytes:

```sh
openssl rand -base64 32
```

`BACKUP_ENCRYPTION_KEY_FILE` works like every other secret's `_FILE` form.

There is **no default and no generated fallback**. An unset
[cursor key](pagination.md) can generate an ephemeral one, because the cost is
cursors that stop verifying after a restart. An unset backup key would produce
a backup nobody can ever read, discovered on the day it is needed.

The key is not part of the configuration the server loads. It is read only by
the three backup commands, so a web replica that will never take a backup does
not hold the key that opens every one of them.

Store it somewhere that is not the machine holding the backups. A key kept
beside the ciphertext defends against nothing that actually happens.

Rotation is a real operation with a real procedure, and it is **not**
re-encryption. See [Key custody and rotation](#key-custody-and-rotation).

## The header

The first few hundred bytes of a backup are readable without the key:

```json
{"magic":"NISEBAK1","format":1,"database":"myapp","schema_version":9,
 "created_at":"2026-09-01T12:00:00Z","pg_dump_version":"pg_dump (PostgreSQL) 17.6"}
```

An operator holding a directory of backups needs to know which database, which
schema, and when. Needing the key to find out is how a restore drill turns
into a search.

It is still authenticated. Its exact bytes are the additional authenticated
data for every sealed chunk, so a header edited to claim a different schema
version makes the body fail to open rather than restoring under a false
description.

There is deliberately no checksum of the plaintext anywhere in the file. The
AEAD is the integrity mechanism, and a second, weaker one beside it would
invite somebody to check the cheap one and call the backup verified.

## Restoring

```sh
./myapp db restore -from /backups/myapp-2026-09-01.backup -confirm myapp
```

`-confirm` takes the **name of the database that is about to be replaced**,
not a bare flag. A boolean would be typed once and thereafter pasted from
shell history, which is the same as not asking — and the usual way a restore
goes wrong is being pointed at the wrong database.

Restore runs `pg_restore --clean --if-exists`, which drops each object the
dump contains before recreating it. It does **not** empty the target first: an
object that exists in the database and not in the backup survives. That is the
difference between "restored" and "restored onto", and it is why a real
recovery restores into a fresh database rather than over a damaged one.

## A backup that fails leaves nothing behind

The file is written under `<path>.partial` and renamed into place only when
the dump has finished and been sealed. An interrupted backup leaves no file
that looks finished — a half-written backup nobody notices is worse than no
backup, because it is the one somebody reaches for.

## JSON output

Every command takes `-json`:

```json
{"action":"verify","path":"/backups/myapp-2026-09-01.backup","database":"myapp",
 "schema_version":9,"created_at":"2026-09-01T12:00:00Z","verified":true}
```

`verified` is set only by `db verify`, and only after a restore succeeded.

## Off-server retention

Taking a backup and keeping one are different problems, and this application
only solves the first. It writes a file. Where that file goes, how long it
lives, and who can delete it are decisions nothing here makes for you — but
they are the decisions that determine whether you have a backup at all.

### A backup on the same host is not a backup

It survives `DROP TABLE`. It does not survive:

- the disk, the volume, or the filesystem failing;
- the server being terminated, by you, by an autoscaler, or by a billing
  problem;
- an attacker with root, who deletes the backups first because they are the
  thing that makes the ransom optional;
- the provider account being lost or suspended.

Every one of those is more common than the one it does protect against.

This is why the encryption matters more than it first appears: **because the
file is sealed before it leaves, the destination does not have to be
trusted.** Object storage at a different provider, a different account, or a
partner's server are all usable, and none of them can read what they hold.
Without that, off-server storage is a decision about trust and stays a plan.

### A schedule that actually runs

```sh
#!/bin/sh
# /usr/local/bin/myapp-backup
set -eu

stamp=$(date -u +%Y%m%dT%H%M%SZ)
file="/var/backups/myapp-${stamp}.backup"

/usr/local/bin/myapp db backup -out "$file"
/usr/local/bin/myapp db verify -from "$file"

rclone copyto "$file" "offsite:myapp-backups/${stamp}.backup"
rclone delete --min-age 30d "offsite:myapp-backups/"

find /var/backups -name 'myapp-*.backup' -mtime +2 -delete
```

Run it from a systemd timer, a cron entry, or the platform's scheduler. The
order is the point:

1. **Back up.**
2. **Verify, before uploading.** A backup that does not restore is worth
   knowing about now rather than during an incident, and there is no reason to
   pay to store one.
3. **Copy off the host.**
4. **Prune the remote by age, and the local copy sooner.** The local file is a
   staging area, not the archive.

Alert on the exit status. A backup job that has been failing quietly for three
weeks is the ordinary way this goes wrong — the pipeline was set up once,
worked, and stopped, and nothing was watching the part that only matters on a
day that has not happened yet.

### How long to keep them

A common ladder is seven daily, four weekly, and six monthly copies. The
number is less important than the reasoning behind it:

**Retention has to be longer than the time it takes you to notice.** Hardware
failure is noticed in minutes. A bad migration, a `DELETE` with a wrong
`WHERE`, or a bug that has been quietly corrupting one column is noticed days
or weeks later — by a user, in a report, or never. If you keep two days of
backups and discover on Thursday that something broke on Monday, every backup
you have contains the broken state.

Long retention is also a data-protection decision: personal data in a backup
is still personal data, and a deletion request is not honoured by a copy you
kept for seven years. Pick a window you can justify in both directions.

### Retention an attacker cannot undo

If the credential that writes the backups can also delete them, then anyone who
compromises the application host can delete the archive — and that is
specifically what ransomware does first.

Pick at least one:

- **Write-only credentials.** The uploader can `PutObject` and nothing else;
  pruning runs from somewhere the application cannot reach.
- **Object lock or immutable retention.** The provider refuses deletion until
  the retention period expires, including from the account owner.
- **Versioning with a lifecycle rule**, so a delete is a tombstone over a copy
  that still exists.
- **A pull-based copy.** A machine the application cannot reach connects in
  and fetches, rather than being pushed to.

### Verify the copy you would actually restore from

The example above verifies before uploading, which catches a bad dump. It does
not catch a truncated upload, a corrupted object, or a bucket somebody
lifecycle-ruled into oblivion.

At least monthly, download from the off-server location and verify **that**
file:

```sh
rclone copyto "offsite:myapp-backups/20260901T030000Z.backup" /tmp/drill.backup
myapp db verify -from /tmp/drill.backup
```

And at least once, restore it into a scratch database by hand and look at the
data — the number of rows in your largest table, the most recent row, a record
you recognize. `db verify` proves the schema arrives; it does not prove that
what you have been backing up is what you thought.

Time it while you do. That number is your recovery time, and it is usually the
first honest one anybody has.

### Where the key lives

The key must not live where the backups live. An archive and its key in the
same bucket is an archive in plaintext with extra steps.

It also must be findable during an outage, by more than one person, without
the systems that are down. A key only the application server has is worth
nothing after the application server is gone, and a key only one person knows
is a single point of failure with opinions and holidays.

A password manager the team already uses, a cloud KMS in a different account,
or a sealed envelope in a safe are all defensible. Whichever it is, write down
where it is somewhere that survives the outage, and check during the restore
drill that the person doing the drill can actually get it.

## Key custody and rotation

Everything above is about a file. This section is about the thirty-two bytes
that decide whether that file is a backup or a large opaque object, and it is
the part most likely to be the reason a restore fails.

### Custody is a set, not a key

The moment you rotate once you hold two keys, and the archives each one opens
have different expiry dates. So the thing to keep is not "the backup key" but a
short table, kept wherever your team keeps secrets:

| Key | In use from | In use until | Opens archives until | Destroy after |
|---|---|---|---|---|
| `2026-01` | 2026-01-14 | 2026-09-01 | last archive created before 2026-09-01 ages out | 2026-10-01 |
| `2026-09` | 2026-09-01 | current | — | — |

The last column is the one people forget. **A retired key must outlive the
archives it opens.** Destroying it the day you rotate turns every existing
backup into an unreadable file, which is the same outcome as never having
taken them and is discovered at the worst possible moment.

Four properties, and each fails in a way that has actually happened somewhere:

- **Not where the backups are.** An archive and its key in the same bucket is
  an archive in plaintext with extra steps.
- **Reachable during an outage, without the systems that are down.** A key
  only the application server has is worth nothing once the application server
  is gone.
- **Reachable by more than one person.** A key one person knows is a single
  point of failure with opinions and holidays.
- **Written down where the *pointer* survives too.** Knowing a key exists and
  not where is the same as not having it. Put the location in the runbook, not
  only in somebody's memory.

A password manager the team already uses, a cloud KMS in a different account
from the backups, or a sealed envelope in a safe are all defensible. What is
not defensible is a `.env` on the backup host.

### Which key opens which archive

Nise stores **no key identifier** in the file, and that is deliberate.

A fingerprint of the key in a cleartext header is an offline oracle: anybody
holding the archive can test candidate keys against it without touching the
ciphertext. An operator-chosen label avoids that but adds a configuration
setting, a format version, and a new way for the label to be wrong.

Neither is needed, because the header already carries the answer:

```json
{"database":"myapp","schema_version":9,"created_at":"2026-09-01T12:00:00Z"}
```

`created_at` is readable without any key. Compare it against your rotation
dates and you have the key — which is exactly what the table above is for, and
why the table is the deliverable rather than a feature.

### Rotation is a cutover, not a rewrite

There is no `db rekey`, and there should not be. Re-encrypting an existing
archive means decrypting it first, which means a plaintext copy of the entire
database existing somewhere — the precise thing this design refuses to do even
during verification, where it would have been convenient. Doing it across a
whole archive, on a schedule, is worse.

So rotation moves forward instead:

```sh
# 1. New key. Same shape as the first one.
openssl rand -base64 32

# 2. Put it wherever your secrets live, and record the date in the key table.

# 3. Switch the backup job to it.
export BACKUP_ENCRYPTION_KEY_FILE=/run/secrets/backup-key-2026-09

# 4. Take a backup and VERIFY it before trusting the new key.
./myapp db backup -out /backups/myapp-2026-09-01.backup
./myapp db verify -from /backups/myapp-2026-09-01.backup

# 5. Keep the old key. Every archive taken before step 3 still needs it.
```

Step 4 is not a formality. A mistyped key, a file with a trailing newline, or
a secret that did not reach the container all produce a backup that appears to
work; `db verify` restores it, and is the only thing that finds out.

After that, the old key expires on its own: archives age out under your
retention policy, and when the last one created before the cutover is gone,
the old key opens nothing and can be destroyed. Write that date in the table
when you rotate, while you know it.

### When to rotate

On a schedule, and on an event.

**Annually** is a defensible schedule for a key that has never left its store.
Rotation is cheap here — one new key, one verified backup — and the value is
mostly that the procedure is one somebody has actually done before the day
they have to.

**Immediately** when the key may have been exposed: it was pasted into a chat,
committed and force-pushed away, printed by a debug log, or held by somebody
who has left. And when you do, be honest about what rotation buys, because it
is less than people assume:

> Rotating does **not** protect the archives already taken. Anybody holding the
> old key and a copy of the old ciphertext can still read them, forever.
> Rotation protects everything from the cutover onwards and nothing before it.

If the exposure is serious, rotation is the first of two steps. The second is
deciding what to do about the archives the old key still opens: delete them and
accept the shortened recovery window, or keep them and accept that they are
readable by whoever has that key. There is no third option, and choosing
between two bad ones quickly is better than discovering later that you never
chose.

### The failure to plan for

**A lost key is lost data.** There is no recovery, no escrow, and no support
channel that can help — the encryption is doing exactly what it was asked to.

This is the argument for the custody rules above, not a disclaimer. Verify
during the restore drill that the person doing the drill can actually retrieve
the key, from the store, without the production systems, on their own. A
custody arrangement nobody has exercised is a belief about a custody
arrangement.

## Point-in-time recovery

A logical dump restores to the moment it was taken. If you back up nightly at
03:00 and lose the database at 17:00, you have lost fourteen hours of writes.
For many applications that is acceptable and worth saying out loud rather than
discovering. For some it is not.

**PITR** — a base backup plus continuously archived write-ahead log, replayed
to a chosen moment — reduces that window to seconds. It is not part of this
application and cannot be: WAL archiving is configured on the PostgreSQL
server, and nothing running as a client can provide it.

### How to get it

- **A managed PostgreSQL provider.** Almost all of them include PITR, and
  this is the cheapest way to have it: somebody else runs the archiving, and
  monitors it.
- **[pgBackRest](https://pgbackrest.org/), [WAL-G](https://github.com/wal-g/wal-g),
  or [Barman](https://pgbarman.org/)** on a self-hosted server. All three do
  base backups, WAL archiving to object storage, retention, and
  restore-to-timestamp.

Do not hand-roll `archive_command` with `cp`. The failure mode is specific and
bad: if archiving fails and nothing notices, PostgreSQL retains WAL until the
data directory fills and the server stops. The tools above handle that; a
shell one-liner does not.

### What it costs

- Storage for a continuous WAL stream, not a nightly file.
- **Monitoring of the archive itself.** An archive that has silently stopped
  is worse than no PITR, because you believe you have it.
- A restore procedure that is materially more involved than
  `db restore -from <file>`, and therefore one that has to be practised.

### It does not replace logical dumps

PITR and `db backup` fail differently, which is why the answer is often both:

| | Logical dump (`db backup`) | PITR |
|---|---|---|
| Recovers from hardware or host loss | Yes, to the last dump | Yes, to seconds |
| Recovers from a bad migration or a wrong `DELETE` | Yes — restore an earlier dump | Yes, if you catch it before it ages out of the WAL retention |
| Survives PostgreSQL major-version upgrades | Yes; restores into a newer server | No; physical backups are version- and platform-locked |
| Portable to another machine, provider, or laptop | Yes | Only to a matching server |
| Readable without the production environment | Yes | No |
| Recovery time | Minutes to hours, proportional to data | Minutes, plus WAL replay |

A physical backup replays corruption faithfully. A logical dump from before the
mistake is the thing that survives "we deleted the wrong rows and noticed on
Thursday". Keep taking them even with PITR configured — they are cheap, and
they are the copy you can open on a laptop.

### Choosing

Ask what losing the interval between backups would actually cost. If the answer
is "some support tickets", nightly dumps are the right amount of engineering,
and dumping more often is the cheapest improvement available. If the answer is
"orders we cannot reconstruct" or "money", PITR is worth its operational cost
— and a managed provider is worth considering before running the archiving
yourself.

## What is not here

- **Re-encrypting an existing archive.** There is no `db rekey`. Rotation is
  a cutover, not a rewrite — see
  [Key custody and rotation](#key-custody-and-rotation) for why that is the
  design rather than a gap.
- **A key registry.** Which key opens which archive is answered by the
  archive's own `created_at` against your rotation dates. Nise stores no key
  identifier and knows nothing about your keys.
- **A scheduler.** This application takes a backup when told to. Running that
  on a timer, alerting when it fails, and pruning the archive are the
  deployment's, and the example above is a starting point rather than a
  supported feature.

## Related

- [Deployment](deployment.md)
- [Configuration](configuration.md)
- [Database migrations](database-migrations.md)
- [Threat model](threat-model.md)
- [Security](security.md)
