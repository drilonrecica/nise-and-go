# The upload lifecycle

Part of the `uploads` compile-time module. It sits on top of
[object storage](storage.md) and is the part that decides whether a stored
byte is allowed to exist.

An uploaded byte reaches a generated application through exactly three steps.

## 1. Stage

The application decides the key. The client never does.

```go
staged, err := uploads.Stage(ctx, uploads.StageRequest{
    OwnerUserID: user.ID,
    Filename:    "holiday photo.png",   // display metadata, not a key
    ContentType: "image/png",           // a claim, checked later
    Size:        1_048_576,             // a claim, checked later
})
```

A row is written saying an upload is expected — by whom, roughly how large, of
roughly what type, and by when — and the object's key is placed under a
`quarantine/` prefix. If the backend can presign, `staged.UploadURL` is a
short-lived URL for that one key; if it cannot, the client uploads through the
application and the server calls `Write`.

The key is derived from a random identifier this step generates. Not from the
filename, not from the user ID, not from a counter. A key a client can
influence is a key a client can collide with somebody else's, and an
unguessable key is half of why a presigned URL is safe to hand out at all.

The checks that can be made here are made here, so an obviously unwanted
upload costs no round trip and no quarantine object: a size outside the
bounds, a missing owner, a declared type outside the allowlist.

## 2. Finalize

The server reads the stored object, measures it, hashes it, and decides what
it actually is.

**Nothing the client said is treated as a fact.**

- The **declared content type** is a claim. A client that wants to store a
  script will call it an image. The real type comes from the leading bytes of
  what was stored, through `http.DetectContentType`. The declared value is
  kept only so the mismatch is visible in the row.
- The **declared size** is a claim. A presigned PUT is a capability to write a
  key, not a promise about length. Signing `Content-Length` removes the easy
  version; measuring the stored object is what enforces the limit.
- The **filename** is display metadata, sanitized at staging and never part of
  a key.
- **"I finished"** is a claim too. An upload staged and never finalized is
  swept.

The object is read once, not twice: a second read of an object a client can
still write to is a second chance for the content to have changed between the
check and the use. The SHA-256 computed during that pass is stored, so a later
change is at least detectable.

If everything passes, the object **moves** out of quarantine to its final key
in one operation and the row becomes `available`. A URL that leaked while the
object was staged stops addressing it.

If anything fails, the object is **deleted** and the row records why. The
reason is for the audit trail and the operator, not for the uploader, who
should not learn which of several checks they failed.

### Ordering

The order of operations is not arbitrary:

- The object is read and measured **before** anything is written to the
  database, so a refusal leaves no half-finalized row.
- The move happens **before** the row is marked available, so a crash between
  them leaves a row that still says `staged` for an object already at its
  final key — which the sweep notices. The other order would leave a row
  claiming an object is available at a key nothing is at.
- A refusal and the sweep both **delete the object first**. A row marked
  rejected beside a surviving object is a quarantine key nothing will look at
  again; a deleted object whose row still says staged is swept on the next
  pass. Only one of those two failures self-corrects.

## 3. Sweep

`uploads_sweep` is a periodic job, registered in `internal/app/jobs.go` and
scheduled every five minutes with the
[database-backed uniqueness](jobs.md) every periodic job gets. It expires
staged uploads whose window elapsed and deletes the objects they were holding
a key for.

It is bounded per run, because a backlog of ten thousand should be worked
through over several runs on the schedule rather than in one run that occupies
a worker for minutes. Running it twice is harmless: the second run finds the
rows the first expired in a state its query does not select.

## The row is the authority

The database decides whether an object is reachable. Not the object store.

A row that says `staged` means not available regardless of what any bucket
policy says, and an object with no row is not available at all. The
`uploads_finalized_state` check constraint means the state and the columns
describing it **cannot** disagree — a row claiming to be available with no
recorded size or checksum is not storable.

## Ownership is not authorization

`Finalize`, `Write`, and `Get` all require the same principal that staged the
upload. A presigned key is unguessable, but unguessable is not authorized: an
identifier that leaked into a log, a referrer, or a support ticket would
otherwise be usable by whoever read it.

A refusal for "not yours" is **byte-identical** to one for "does not exist",
because whether an identifier exists is itself information.

The application's own permission check is separate, and happens where the
application decides — this layer establishes only that the capability being
used belongs to the principal using it.

## The accepted types

`DefaultAcceptedTypes` is images and PDFs. It is an allowlist and it is small,
because the alternative — accepting everything and hoping the serving path
sets the right headers — is how a file upload becomes stored cross-site
scripting.

Widen it deliberately, with `uploads.WithAcceptedTypes`, and remember that
every entry is a type some browser will render.

## Malware scanning

**A generated application ships no malware scanner, and says so.** See
[ADR 0027](adr/0027-upload-malware-scanning-boundary.md) for the reasoning;
the short version is that bundling an engine would put a daemon and several
hundred megabytes into every project that accepts a profile picture, keeping
its signatures current would mean fetching them, and a scanner with stale
signatures is worse than none because it produces a "clean" verdict somebody
believes.

What the framework provides is the boundary:

```go
type Scanner interface {
    Name() string
    Scan(ctx context.Context, r io.Reader) error
}

uploads.New(transactor, store, uploads.WithScanner(myScanner))
```

The scan runs **inside `Finalize`, after the cheap checks and before the
move**. That position is the decision:

- After the type and size checks, so an object that was going to be refused
  anyway never costs a scan.
- Before the move, so an object a scanner refuses has never existed at a
  reachable key. There is no window in which it is available, and no cleanup
  to get wrong.
- Over the stored bytes, for the same reason everything else in this lifecycle
  is.

### "Could not check" is not "malicious"

An implementation returns an error wrapping `uploads.ErrScanUnavailable` when
the scanner itself could not run — the daemon is down, the socket refused, the
timeout elapsed.

That is treated as a failure to finalize, not as a rejection. The upload stays
staged, the object stays in quarantine, and finalization can be retried once
the scanner is back. **Deleting somebody's file on the strength of a broken
daemon is data loss caused by an outage**, and the two facts are different
enough that the interface makes you say which one you mean.

### What is recorded

`uploads.scanned_by` holds the scanner's name, or NULL. It exists so that "was
this scanned, and by what" is answerable for an object that is already
available — the question asked after a scanner is added, and after one turns
out to have been broken for a month.

### The residual risk, stated

With no scanner configured, a generated application stores whatever passes its
type and size checks. A PDF can be a real PDF and still carry an exploit for a
reader; a PNG can be a real PNG and still be the payload half of something
else. That risk is real, it is not mitigated by anything on this page, and it
belongs in every deployment's own threat model.
