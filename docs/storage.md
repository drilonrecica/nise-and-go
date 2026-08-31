# Object storage

Object storage is the `uploads` compile-time module. A project generated
without it has no `internal/platform/storage/` directory, no `STORAGE_*`
variables, and no code that mentions either — not a disabled subsystem, an
absent one.

Two implementations satisfy one interface: `Local`, which writes to a
directory, and `S3`, which speaks the S3 API to any compatible service.
Moving between them is a configuration change.

## Keys are not paths

A key looks like a path and is not one. It is an opaque identifier the
application chose, and the single most dangerous thing this package can do is
let a key that came from a user decide where a byte lands.

`ValidateKey` is therefore strict to the point of being inconvenient:

- ASCII letters, digits, `-`, `_`, `.`, and `/`. Nothing else.
- No leading or trailing separator, no empty component, no `.` or `..`
  component.
- Already in canonical form — a key that `path.Clean` would change is
  **refused, not cleaned**. Cleaning would mean two different key strings
  addressing one object, and an authorization check performed on the key the
  caller passed rather than on the object it reached.
- At most 512 bytes, and at most 200 per component (most filesystems refuse a
  longer name, and the local store has to be able to write what S3 accepted).

The character set is an allowlist rather than a denylist of dangerous
characters. A denylist has to stay complete against every filesystem, URL
parser, and S3 clone that will ever see the key, and it only has to be
incomplete once.

Every operation on both backends validates before it does anything.

## Filenames are not keys either

A browser-supplied filename is display metadata. It can be empty, a duplicate,
three hundred characters of Unicode, `../../etc/passwd`, or a name whose
extension disagrees with its content. Storing under it hands key naming to
whoever is uploading.

Derive the key from an identifier the application owns, and keep
`storage.SanitizeFilename(name)` beside it in the database as the download
name. What comes back is the base name only, restricted to the key character
set, bounded, and never empty — an unusable name becomes `file`.

## Local

Writes files under one directory, at mode `0600` inside directories at `0700`.
The modes are set explicitly rather than left to the umask, because the
process that set the umask is not the process that will read the file, and a
store readable by every account on a shared host is a data leak nothing
reports.

**Every operation goes through `os.Root`.** A key that somehow passed
validation still cannot escape the directory, because the confinement is the
operating system's rather than this package's care. That belt-and-braces is
deliberate: path validation is exactly the kind of code that is correct until
somebody adds a case to it, and a symlink planted inside the directory is an
escape validation cannot see at all — the key is well formed, and the
redirection happens in the filesystem after the check.

Writes are atomic. Content goes to a temporary name in the same directory and
is renamed over the target once complete, so a reader arriving mid-write sees
either the previous object or the new one. A failed write leaves neither a
truncated object nor the temporary file.

It is a legitimate production choice for a single node with a real volume
behind it. It is not a way to run more than one replica: two processes on
different machines writing to different disks will disagree, and nothing here
will tell them.

## S3

`internal/platform/storage/sigv4.go` is a hand-written AWS Signature Version 4
implementation, and `s3.go` is four HTTP verbs over it.

This is not the AWS SDK because the AWS SDK is a very large amount of
generated code, a credential chain, retry middleware, and a plugin interface,
for `PUT`, `GET`, `HEAD`, and `DELETE`. The signing algorithm is specified
precisely and does not change; its difficulty is entirely in getting the
canonicalization exactly right.

Which is why it is verified against a real service and not only against
itself. `TestS3AgainstARealService` runs the whole round trip against any
S3-compatible endpoint:

```
podman run --rm -p 9000:9000 \
    -e MINIO_ROOT_USER=minio -e MINIO_ROOT_PASSWORD=miniosecret \
    quay.io/minio/minio server /data
```

then set `TEST_S3_ENDPOINT`, `TEST_S3_BUCKET`, `TEST_S3_ACCESS_KEY_ID`, and
`TEST_S3_SECRET_ACCESS_KEY`. It **skips** when they are unset, including under
CI — unlike the PostgreSQL suites, which fail. That difference is deliberate:
a database is required for the application to work at all, while object
storage is an optional module many deployments will configure as `local` and
never point at a service.

Three things about the signature are worth knowing if you touch that file:

- **The Host header is signed from `Request.Host`**, not from `Request.Header`
  — Go keeps it there, and a loop over headers alone omits it. That is the
  most common way a hand-written SigV4 produces a rejected signature.
- **Only `content-type` and the `x-amz-*` family are signed.** Signing
  everything would make the signature depend on headers a proxy adds in
  transit, so every request through that proxy would fail authentication.
- **Path segments are encoded by this file, not by `url.URL`.** Go leaves
  several characters unescaped that SigV4 requires encoded, and one differing
  byte is a rejected request whose error message names none of this.

`S3_PATH_STYLE` defaults to **true**, the opposite of the AWS SDK's default,
because every S3-compatible service that is not AWS serves one hostname. Set
it to `false` for AWS itself.

The body is sent with an unsigned payload hash. Signing it would require the
whole object in memory or on disk to hash before sending, which turns an
upload of any size into a buffer of that size; the request itself is still
authenticated, covering the method, path, headers, and credential scope.

## What the interface deliberately lacks

There is no `List`. An application that needs to enumerate objects has a
database table describing them, and reconstructing that from a bucket listing
is how a storage backend becomes a second source of truth that disagrees with
the first.

`ContentType` in `PutOptions` should come from the application's own
validation of the content, never from a browser-supplied header. A store that
echoes an attacker-chosen `Content-Type` back to a browser is stored
cross-site scripting with extra steps.

See [configuration](configuration.md) for the variables.
