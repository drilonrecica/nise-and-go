// Package forwarded decides what a request's real client address, scheme, and
// host are when the application sits behind a reverse proxy.
//
// # Why this needs a policy at all
//
// `X-Forwarded-For` is a list any client can write. A request arriving with
// `X-Forwarded-For: 10.0.0.1` came from whoever sent it, not from 10.0.0.1.
// Reading the leftmost entry — the most common mistake in this area — hands an
// attacker complete control of the address every rate limiter, audit record,
// and geo-restriction in the application will use.
//
// The list is only meaningful from the right. Each proxy appends the address it
// received the request from, so the rightmost entry was written by the proxy
// closest to this server, the one before it by the proxy before that, and so on
// until the entries stop being ones this deployment produced. How far to count
// is not something a library can know; it is a fact about the deployment's
// topology, and this package requires it to be declared.
//
// # The two questions
//
// A [Policy] answers exactly two: how many proxies are in front of this
// application, and which peers are allowed to be one. Both default to the
// answers that trust nothing — zero hops, no networks — so an application that
// never configures a proxy reads `RemoteAddr` and ignores every forwarded
// header, which is correct for a service exposed directly.
//
// The hop count is what makes a forged prefix harmless: counting from the right
// skips exactly the entries this deployment's own proxies wrote, and anything
// the client invented sits to the left of them, unread. The network list is the
// second gate, for the case where the application's port becomes reachable
// without going through the proxy at all: a peer outside it is not a proxy, so
// its forwarded headers are ignored no matter what they say.
//
// # Falling back rather than guessing
//
// A trusted request whose header is shorter than the declared hop count is a
// misconfiguration, not an attack to parse around. [Policy.Resolve] falls back
// to the connection's own address and reports [Request.Trusted] as false, so a
// caller can log it and a rate limiter still has a real address to work with.
package forwarded
