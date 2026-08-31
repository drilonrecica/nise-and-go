package forwarded_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/forwarded"
)

func testPolicy(t *testing.T, hops int, networks ...string) forwarded.Policy {
	t.Helper()

	var prefixes []netip.Prefix
	if len(networks) > 0 {
		parsed, err := forwarded.ParseNetworks(strings.Join(networks, ","))
		if err != nil {
			t.Fatalf("ParseNetworks: %v", err)
		}
		prefixes = parsed
	}
	policy, err := forwarded.NewPolicy(hops, prefixes)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return policy
}

func request(t *testing.T, remoteAddr string, headers map[string][]string) *http.Request {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, "http://app.example/path", nil)
	r.RemoteAddr = remoteAddr
	r.Host = "app.example"
	for name, values := range headers {
		r.Header[http.CanonicalHeaderKey(name)] = values
	}
	return r
}

func TestNewPolicyBounds(t *testing.T) {
	t.Parallel()

	for _, hops := range []int{-1, forwarded.MaxHops + 1} {
		if _, err := forwarded.NewPolicy(hops, nil); !errors.Is(err, forwarded.ErrHops) {
			t.Errorf("NewPolicy(%d) error = %v, want ErrHops", hops, err)
		}
	}
	if _, err := forwarded.NewPolicy(0, nil); err != nil {
		t.Errorf("the trust-nothing policy was refused: %v", err)
	}

	// Declaring networks without declaring a proxy would read nothing, which
	// is a configuration the operator did not mean.
	networks, err := forwarded.ParseNetworks("10.0.0.0/8")
	if err != nil {
		t.Fatalf("ParseNetworks: %v", err)
	}
	if _, err := forwarded.NewPolicy(0, networks); !errors.Is(err, forwarded.ErrNetworks) {
		t.Errorf("NewPolicy accepted networks with no hops: %v", err)
	}
	if _, err := forwarded.NewPolicy(1, []netip.Prefix{{}}); !errors.Is(err, forwarded.ErrNetworks) {
		t.Errorf("NewPolicy accepted an invalid prefix: %v", err)
	}
	// A prefix with host bits set is almost always a typo for the block the
	// operator meant, and silently masking it would hide the typo.
	misaligned, err := netip.ParsePrefix("10.1.2.3/8")
	if err != nil {
		t.Fatalf("ParsePrefix: %v", err)
	}
	if _, err := forwarded.NewPolicy(1, []netip.Prefix{misaligned}); !errors.Is(err, forwarded.ErrNetworks) {
		t.Errorf("NewPolicy accepted a prefix with host bits set: %v", err)
	}
}

func TestParseNetworks(t *testing.T) {
	t.Parallel()

	networks, err := forwarded.ParseNetworks(" 10.0.0.0/8 , 192.168.1.7 , fd00::/8 ")
	if err != nil {
		t.Fatalf("ParseNetworks: %v", err)
	}
	if len(networks) != 3 {
		t.Fatalf("parsed %d networks, want 3", len(networks))
	}
	// A bare address becomes a single-host prefix, because that is what an
	// operator means when there is exactly one proxy.
	if networks[1].Bits() != 32 || networks[1].Addr().String() != "192.168.1.7" {
		t.Errorf("bare address parsed as %s", networks[1])
	}

	if got, err := forwarded.ParseNetworks("  "); err != nil || got != nil {
		t.Errorf("ParseNetworks(blank) = %v, %v; want no networks", got, err)
	}
	for _, value := range []string{"10.0.0.0/8,", "not-an-address", "10.0.0.0/33", "10.0.0.0/8,,192.168.0.0/16", "10.1.2.3/8"} {
		if _, err := forwarded.ParseNetworks(value); !errors.Is(err, forwarded.ErrNetworks) {
			t.Errorf("ParseNetworks(%q) error = %v, want ErrNetworks", value, err)
		}
	}
}

func TestZeroHopsIgnoresEveryForwardedHeader(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t, 0)
	if policy.TrustsForwardedHeaders() {
		t.Error("the zero-hop policy trusts forwarded headers")
	}
	resolved := policy.Resolve(request(t, "203.0.113.9:44321", map[string][]string{
		"X-Forwarded-For":   {"1.2.3.4"},
		"X-Forwarded-Proto": {"https"},
		"X-Forwarded-Host":  {"evil.example"},
	}))
	if resolved.ClientIP.String() != "203.0.113.9" {
		t.Errorf("ClientIP = %s, want the connection's own peer", resolved.ClientIP)
	}
	if resolved.Scheme != "http" || resolved.Host != "app.example" {
		t.Errorf("resolved = %#v, want the request's own scheme and host", resolved)
	}
	if resolved.Trusted {
		t.Error("a request under the zero-hop policy reported trusted headers")
	}
}

func TestOneHopReadsTheEntryTheProxyWrote(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t, 1)

	// The leftmost entry is whatever the client invented. Counting from the
	// right skips exactly the entry this deployment's proxy appended, and the
	// one before it is the address that proxy received the request from.
	resolved := policy.Resolve(request(t, "10.0.0.1:5000", map[string][]string{
		"X-Forwarded-For": {"1.1.1.1, 203.0.113.9"},
	}))
	if resolved.ClientIP.String() != "203.0.113.9" {
		t.Fatalf("ClientIP = %s, want the address the proxy reported", resolved.ClientIP)
	}
	if !resolved.Trusted {
		t.Error("a well-formed forwarded request was not trusted")
	}

	// The same request with a forged prefix resolves identically: the forged
	// entries are to the left of the count and are never read.
	forged := policy.Resolve(request(t, "10.0.0.1:5000", map[string][]string{
		"X-Forwarded-For": {"127.0.0.1, 10.0.0.5, 8.8.8.8, 203.0.113.9"},
	}))
	if forged.ClientIP.String() != "203.0.113.9" {
		t.Fatalf("ClientIP = %s; a forged prefix changed the answer", forged.ClientIP)
	}
}

func TestHopCountingUsesTheRightEntry(t *testing.T) {
	t.Parallel()

	chain := map[string][]string{
		"X-Forwarded-For": {"9.9.9.9, 203.0.113.9, 172.16.0.2, 10.0.0.2"},
	}
	// Each proxy appends the address it received the request from, so the
	// last entry is what the proxy closest to this server wrote. Counting N
	// from the right therefore lands on the address the outermost of N
	// trusted proxies saw, and everything to its left is client-supplied.
	wants := map[int]string{
		1: "10.0.0.2",
		2: "172.16.0.2",
		3: "203.0.113.9",
		4: "9.9.9.9",
	}
	for hops, want := range wants {
		policy := testPolicy(t, hops)
		resolved := policy.Resolve(request(t, "10.0.0.1:5000", chain))
		if resolved.ClientIP.String() != want {
			t.Errorf("hops=%d: ClientIP = %s, want %s", hops, resolved.ClientIP, want)
		}
		if !resolved.Trusted {
			t.Errorf("hops=%d: not trusted", hops)
		}
	}

	// Repeated headers and a comma list are the same thing to HTTP, so they
	// must count the same.
	split := testPolicy(t, 2).Resolve(request(t, "10.0.0.1:5000", map[string][]string{
		"X-Forwarded-For": {"9.9.9.9, 203.0.113.9", "172.16.0.2", "10.0.0.2"},
	}))
	if split.ClientIP.String() != "172.16.0.2" {
		t.Errorf("split headers resolved to %s", split.ClientIP)
	}
}

func TestShortChainFallsBackRatherThanGuessing(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t, 2)
	tests := map[string][]string{
		"no header at all":                     nil,
		"an empty header":                      {""},
		"one entry short":                      {"10.0.0.2"},
		"the selected entry is not an address": {"10.0.0.2, not-an-address, 10.0.0.3"},
	}
	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			headers := map[string][]string{}
			if values != nil {
				headers["X-Forwarded-For"] = values
			}
			resolved := policy.Resolve(request(t, "10.0.0.1:5000", headers))
			if resolved.ClientIP.String() != "10.0.0.1" {
				t.Errorf("ClientIP = %s, want the connection's own peer", resolved.ClientIP)
			}
			if resolved.Trusted {
				t.Error("a short or unparseable chain was reported as trusted")
			}
		})
	}
}

func TestNetworksGateWhoMayBeAProxy(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t, 1, "10.0.0.0/8", "fd00::/8")

	trusted := policy.Resolve(request(t, "10.0.0.1:5000", map[string][]string{
		"X-Forwarded-For": {"203.0.113.9"},
	}))
	if !trusted.Trusted || trusted.ClientIP.String() != "203.0.113.9" {
		t.Fatalf("a permitted peer resolved to %#v", trusted)
	}

	// The same headers from anywhere else are ignored entirely. This is the
	// gate that matters when the application's port becomes reachable without
	// going through the proxy.
	untrusted := policy.Resolve(request(t, "198.51.100.5:5000", map[string][]string{
		"X-Forwarded-For": {"203.0.113.9"},
	}))
	if untrusted.Trusted {
		t.Error("a peer outside the permitted networks was trusted")
	}
	if untrusted.ClientIP.String() != "198.51.100.5" {
		t.Errorf("ClientIP = %s, want the untrusted peer's own address", untrusted.ClientIP)
	}

	ipv6 := policy.Resolve(request(t, "[fd00::1]:5000", map[string][]string{
		"X-Forwarded-For": {"2001:db8::1"},
	}))
	if !ipv6.Trusted || ipv6.ClientIP.String() != "2001:db8::1" {
		t.Errorf("IPv6 peer resolved to %#v", ipv6)
	}
}

func TestForwardedAddressForms(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t, 1)
	accepted := map[string]string{
		"203.0.113.9":        "203.0.113.9",
		"203.0.113.9:1234":   "203.0.113.9",
		"2001:db8::1":        "2001:db8::1",
		"[2001:db8::1]:443":  "2001:db8::1",
		"  203.0.113.9  ":    "203.0.113.9",
		"::ffff:203.0.113.9": "203.0.113.9",
	}
	for header, want := range accepted {
		resolved := policy.Resolve(request(t, "10.0.0.1:5000", map[string][]string{"X-Forwarded-For": {header}}))
		if !resolved.Trusted || resolved.ClientIP.String() != want {
			t.Errorf("%q resolved to %s (trusted=%t), want %s", header, resolved.ClientIP, resolved.Trusted, want)
		}
	}

	rejected := []string{
		"unknown",
		"_hidden",
		"203.0.113.9 10.0.0.2",
		strings.Repeat("a", forwarded.MaxHostBytes+1),
	}
	for _, header := range rejected {
		resolved := policy.Resolve(request(t, "10.0.0.1:5000", map[string][]string{"X-Forwarded-For": {header}}))
		if resolved.Trusted {
			t.Errorf("%q was accepted as an address", header)
		}
	}
}

func TestOversizedHeadersAreRefusedBeforeParsing(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t, 1)

	long := policy.Resolve(request(t, "10.0.0.1:5000", map[string][]string{
		"X-Forwarded-For": {strings.Repeat("10.0.0.2, ", 200) + "10.0.0.3"},
	}))
	if long.Trusted {
		t.Error("an oversized forwarded header was parsed")
	}

	many := make([]string, 0, forwarded.MaxForwardedEntries+2)
	for range forwarded.MaxForwardedEntries + 2 {
		many = append(many, "10.0.0.2")
	}
	crowded := policy.Resolve(request(t, "10.0.0.1:5000", map[string][]string{"X-Forwarded-For": many}))
	if crowded.Trusted {
		t.Error("a header with more entries than the bound was parsed")
	}
}

func TestSchemeAndHostAreValidated(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t, 1)
	resolved := policy.Resolve(request(t, "10.0.0.1:5000", map[string][]string{
		"X-Forwarded-For":   {"203.0.113.9"},
		"X-Forwarded-Proto": {"https"},
		"X-Forwarded-Host":  {"public.example"},
	}))
	if resolved.Scheme != "https" || resolved.Host != "public.example" {
		t.Fatalf("resolved = %#v", resolved)
	}

	// A scheme or host outside the accepted shape leaves the request's own
	// value in place rather than propagating something a URL builder would
	// later split on.
	hostile := policy.Resolve(request(t, "10.0.0.1:5000", map[string][]string{
		"X-Forwarded-For":   {"203.0.113.9"},
		"X-Forwarded-Proto": {"javascript"},
		"X-Forwarded-Host":  {"evil.example/path?x=1"},
	}))
	if hostile.Scheme != "http" {
		t.Errorf("Scheme = %q, want the request's own scheme", hostile.Scheme)
	}
	if hostile.Host != "app.example" {
		t.Errorf("Host = %q, want the request's own host", hostile.Host)
	}
}

func TestMiddlewareResolvesOnce(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t, 1)
	var (
		seen       forwarded.Request
		present    bool
		fromHelper netip.Addr
	)
	handler := policy.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, present = forwarded.FromContext(r.Context())
		fromHelper = forwarded.ClientIP(r.Context())
	}))
	handler.ServeHTTP(httptest.NewRecorder(), request(t, "10.0.0.1:5000", map[string][]string{
		"X-Forwarded-For": {"1.1.1.1, 203.0.113.9"},
	}))

	if !present {
		t.Fatal("the middleware stored nothing")
	}
	if seen.ClientIP.String() != "203.0.113.9" || fromHelper != seen.ClientIP {
		t.Fatalf("resolved = %#v, helper = %s", seen, fromHelper)
	}

	// Without the middleware there is no address at all, so a caller that
	// rate-limits by one has to treat the wiring failure as a failure rather
	// than as a bucket every request shares.
	if forwarded.ClientIP(httptest.NewRequest(http.MethodGet, "/", nil).Context()).IsValid() {
		t.Error("ClientIP invented an address without the middleware")
	}
}

func TestResolveAlwaysProducesAnAddress(t *testing.T) {
	t.Parallel()

	// A RemoteAddr Go would not normally produce still must not leave the
	// caller with something that silently compares equal across requests.
	policy := testPolicy(t, 0)
	for _, remote := range []string{"203.0.113.9:1", "203.0.113.9", "[2001:db8::1]:8080"} {
		if !policy.Resolve(request(t, remote, nil)).ClientIP.IsValid() {
			t.Errorf("RemoteAddr %q produced no address", remote)
		}
	}
	if policy.Resolve(request(t, "", nil)).ClientIP.IsValid() {
		t.Error("an empty RemoteAddr produced an address")
	}
}
