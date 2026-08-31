# Outbound email

A generated application has a mail interface, an SMTP transport, and embedded
message templates. The surface is deliberately small: `Mailer` has one method,
`Message` has six fields, and there is no queue, no template registry, and no
provider abstraction beyond the one interface.

## Send from a job, not from a request

An SMTP conversation is a network round trip to a machine the application does
not control. Putting one in the path of a form submission makes that form as
slow and as available as the mail provider — and makes "the invitation email
was slow" indistinguishable from "creating the account failed".

Sending belongs in a background job, where a provider outage delays mail
rather than failing user-visible work, and where the retry schedule in
[background jobs](jobs.md) already applies. Enqueue it in the same transaction
as the change that requires it, and the email cannot be sent for a change that
rolled back.

## The interface

```go
type Mailer interface {
    Send(ctx context.Context, m Message) error
}
```

A use case takes a `Mailer`. Its test satisfies that with `mail.MailerFunc` in
four lines and needs no mail server; production passes `*mail.SMTP`. Nothing
downstream branches on which transport is configured — that choice is made
once, in `internal/app/mail.go`.

## Transports

**`mail.SMTP`** talks to a real server, over STARTTLS (port 587), implicit TLS
(port 465), or in the clear.

**`mail.Log`** records that a message would have been sent and sends nothing.
It is what a developer with no mail server wants, and production refuses to
start with it. It validates every message exactly as the SMTP transport does,
so a message that would be refused in production is refused in development
too — and it never writes the body, because a rendered message routinely
carries a single-use token, and a development log is the least protected place
one of those could end up.

See [configuration](configuration.md) for the variables.

## What is refused

Every field of a `Message` becomes a header, and **a bare newline in a header
value ends that header and begins another**. That is how a value which reached
the application from a form becomes a `Bcc`, a forged `From`, or an entire
second message. `Message.Validate` rejects `\r`, `\n`, and `NUL` in the
subject and in every address, and rejects rather than strips: stripping
silently would send a message nobody wrote.

`NUL` is included because a C-level SMTP implementation on the far side may
truncate there, turning one message into a different one.

Addresses are parsed with `net/mail`, which accepts a display name —
`Support <support@example.com>` is a legitimate sender — and does not accept a
list, so a single address field smuggling a second recipient is refused rather
than delivered.

Also refused: no recipients, more than 50 recipients (a mailing list wants
per-recipient sending, with its own unsubscribe handling and bounce
attribution), a subject over 512 bytes, and a message with neither a text nor
an HTML body.

## Templates

Message templates live in `internal/platform/mail/templates/` and are embedded
in the binary, for the same reason the frontend and the migrations are: a
deployment is one file, and a template missing at run time is a message that
cannot be sent to a user who is waiting for it. They are parsed once, when the
renderer is built, so a typo is a startup failure rather than something the
first affected user discovers.

They come in pairs sharing a base name:

- `invitation.txt` — the plain-text part, rendered with `text/template`.
- `invitation.html` — the HTML part, rendered with `html/template`.

**The two engines are different on purpose.** `text/template` does no
escaping, which is right for a plain-text body and catastrophic for markup;
`html/template` escapes contextually, so a name containing a `<` arrives as a
name rather than as a tag, and a `javascript:` URL never survives into an
`href`.

A text part is required and the HTML part is optional, and a text template
with no HTML sibling is a startup error. A message with only an HTML part is
filtered more aggressively, unreadable in a text client, and unreadable to a
screen reader that has given up on the markup.

```go
message, err := renderer.Render("invitation", "You have been invited",
    []string{user.Email}, map[string]string{
        "AppName":   "Example",
        "AcceptURL": acceptURL,
    })
```

The subject is a parameter rather than a line inside the template. A subject
living in the template would have to be extracted from the rendered output by
convention — a first line, a comment, a defined block — and every one of those
conventions breaks quietly when somebody edits the template.

Templates are parsed with `missingkey=error`, so a template referencing a key
its data does not have fails the render instead of putting `<no value>` into a
message somebody receives.

## Wire format

The message is built as `multipart/alternative` when both parts are present,
with the plain-text part **first**: a client renders the last alternative it
understands, so text after HTML would show the text to everyone.

Non-ASCII subjects are encoded as RFC 2047 encoded-words, so they arrive
readable rather than as mojibake.

Dot stuffing — escaping a line consisting of a single `.`, which is the
sequence that ends SMTP `DATA` — is performed by `net/smtp`'s own
`textproto.DotWriter` and deliberately **not** repeated in this package.
Stuffing twice would put an extra dot into every message whose body starts a
line with one. The transport's tests assert the bytes that cross the
connection and check that the stuffing happened exactly once, which is how
that bug was found.
