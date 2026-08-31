// Package pagination implements authenticated, versioned cursor tokens and the
// query parsing that goes with them.
//
// A cursor is an opaque string the server issues and the client returns
// verbatim. It carries the position of the last row a page ended on, the
// direction that page was read in, an expiry, and a fingerprint of the query
// the page belonged to. All of that is covered by an HMAC-SHA256 tag, so a
// client can neither read a position out of a token nor invent one.
//
// The tag matters for more than tidiness. A raw position ("id > 41812") is an
// invitation to edit: a client that can change it, or replay yesterday's
// cursor against today's filters, can walk rows the current query was never
// supposed to return. Binding the filters into the token is what makes that a
// refusal rather than a silent wrong answer.
//
// # Token format
//
//	nc1.<key-id>.<base64url payload>.<base64url tag>
//
// The version prefix and key identifier are outside the payload because both
// are needed before the payload can be trusted: the version selects the
// parser, and the identifier selects the verification key. Everything a
// decoder acts on afterwards is inside the authenticated payload.
//
// # Key rotation
//
// A [KeyRing] has exactly one active key, which signs, and any number of
// retired keys, which only verify. Rotation is therefore a two-step
// deployment with no window in which outstanding cursors break: promote a new
// active key while keeping the previous one retired, then drop the retired key
// once every cursor issued under it has expired.
//
// # Failure modes
//
// Decoding distinguishes its failures — [ErrCursorMalformed],
// [ErrCursorVersion], [ErrCursorSignature], [ErrCursorExpired],
// [ErrCursorBinding], and [ErrCursorDirection] — so an application can log
// which invariant a request broke. Every one of them is a client error; none
// of them should reach a caller as anything but one bounded public failure.
package pagination
