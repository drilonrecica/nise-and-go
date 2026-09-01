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

> Key rotation — re-encrypting an archive under a new key, and what to do with
> the old one — is not implemented and not documented yet. It is tracked as an
> open question in the [threat model](threat-model.md).

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

## What is not here

- **Retention, rotation, and off-server copies.** Taking a backup and keeping
  it are different problems; see [deployment](deployment.md).
- **Point-in-time recovery.** A logical dump restores to the moment it was
  taken. PITR needs WAL archiving, which is a property of the PostgreSQL
  deployment rather than of this application.
- **Key rotation.** See above.

## Related

- [Deployment](deployment.md)
- [Configuration](configuration.md)
- [Database migrations](database-migrations.md)
- [Threat model](threat-model.md)
- [Security](security.md)
