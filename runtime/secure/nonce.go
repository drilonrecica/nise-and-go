package secure

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
)

// nonceByteLength is how much randomness a nonce carries. The CSP
// specification requires at least 128 bits of entropy per nonce; 16 bytes is
// exactly that, and encodes to 22 characters.
const nonceByteLength = 16

// contextKey is an unexported type so the key this package puts in a
// context.Context cannot collide with a key defined by another package.
type contextKey int

const nonceContextKey contextKey = iota

// nonceState is the per-request nonce slot the middleware puts in the request
// context. The nonce is minted on first use rather than on every request, so
// that a handler which serves an immutable build asset neither spends
// randomness nor forces its response out of shared caches.
type nonceState struct {
	enabled bool

	mu    sync.Mutex
	value string
}

// get returns the request's nonce, minting one on first call. It reports
// false, with an empty nonce, for a policy whose CSP has no script-src to put
// a nonce into — the API policy.
func (n *nonceState) get() (string, bool) {
	if n == nil || !n.enabled {
		return "", false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.value == "" {
		n.value = newNonce()
	}
	return n.value, true
}

// minted returns the nonce if one has already been minted for this request,
// without minting one.
func (n *nonceState) minted() (string, bool) {
	if n == nil {
		return "", false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.value, n.value != ""
}

// newNonce returns a fresh CSP nonce.
func newNonce() string {
	b := make([]byte, nonceByteLength)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read fails only if the system CSPRNG is unavailable, a
		// condition this package cannot recover from. Falling back to a
		// weaker source would hand an attacker a guessable nonce and turn
		// the Content-Security-Policy into decoration, which is precisely
		// the silent downgrade of a security property the project's
		// fail-closed constraint forbids. Panic rather than return a
		// predictable nonce.
		panic("runtime/secure: crypto/rand unavailable: " + err.Error())
	}
	// The CSP base64-value grammar accepts the URL-safe alphabet, so this
	// keeps '+' and '/' out of a header value without losing any entropy.
	return base64.RawURLEncoding.EncodeToString(b)
}

// Nonce returns the Content-Security-Policy nonce for the request carrying
// ctx, minting it on the first call and returning the same value for every
// later call on the same request.
//
// A handler that renders HTML puts the returned value in the nonce attribute
// of every inline script it emits:
//
//	nonce, ok := secure.Nonce(r.Context())
//	if !ok {
//	    http.Error(w, "server misconfigured", http.StatusInternalServerError)
//	    return
//	}
//	fmt.Fprintf(w, `<script nonce=%q>…</script>`, nonce)
//
// Nonce reports false when there is no nonce to give: the request did not
// pass through [Middleware], or it did but under a policy whose CSP has no
// script-src to carry a nonce (see [NewAPIPolicy]). A handler must treat
// false as an error rather than emitting an empty nonce attribute, which
// would not match and would be blocked.
//
// # Minting is lazy, and that is a security property
//
// The nonce is created only when a handler asks for it, and the
// Content-Security-Policy header names the nonce only if one was minted. A
// handler that serves a hashed, immutable build asset never calls Nonce, so
// its response is not made per-user and stays cacheable. A response that does
// carry a nonce is sent with Cache-Control: no-store, because a nonce served
// from a shared cache is a nonce every visitor — including an attacker
// probing for an injection point — already knows, which defeats the whole
// mechanism.
//
// # Call it from the request's own goroutine
//
// Nonce is safe to call concurrently, but the value must be minted before the
// response headers are written. Calling it from a goroutine that outlives the
// handler races with the header write and may produce a nonce the header does
// not name.
func Nonce(ctx context.Context) (string, bool) {
	state, ok := ctx.Value(nonceContextKey).(*nonceState)
	if !ok {
		return "", false
	}
	return state.get()
}
