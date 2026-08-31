package forwarded

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// Headers this package reads. RFC 7239's `Forwarded` is deliberately not one of
// them: supporting two mechanisms for the same fact doubles the surface a proxy
// has to strip correctly, and every proxy this project documents writes the
// X-Forwarded-* set.
const (
	// HeaderFor carries the appended client addresses.
	HeaderFor = "X-Forwarded-For"
	// HeaderProto carries the scheme the client used.
	HeaderProto = "X-Forwarded-Proto"
	// HeaderHost carries the host the client addressed.
	HeaderHost = "X-Forwarded-Host"
)

// Bounds on what will be parsed. They exist so a hostile header costs a length
// comparison rather than an allocation proportional to what an attacker sent.
const (
	// MaxHops is the largest proxy chain that may be declared. A deployment
	// with more than this in front of it has a topology problem, not a
	// configuration one.
	MaxHops = 8
	// MaxForwardedEntries bounds how many list entries are parsed.
	MaxForwardedEntries = 64
	// MaxHeaderBytes bounds a single forwarded header value.
	MaxHeaderBytes = 4096
	// MaxHostBytes bounds an accepted forwarded host.
	MaxHostBytes = 255
)

// Errors reported by policy construction.
var (
	// ErrHops reports a hop count outside 0..MaxHops.
	ErrHops = errors.New("trusted proxy hop count is outside the permitted bounds")
	// ErrNetworks reports an unusable trusted-peer network list.
	ErrNetworks = errors.New("trusted proxy networks are not usable")
)

// Policy declares how the edge in front of this application is trusted.
//
// The zero value trusts nothing, which is the correct policy for a service
// exposed directly, and is what an application gets by not configuring one.
type Policy struct {
	hops     int
	networks []netip.Prefix
}

// NewPolicy declares the proxy chain.
//
// hops is the number of reverse proxies between the client and this process.
// Zero means there are none: forwarded headers are ignored entirely, whatever
// they contain.
//
// networks, when non-empty, is the set of peers permitted to be that proxy. A
// request arriving from anywhere else has its forwarded headers ignored. Leave
// it empty only when the application's port cannot be reached except through
// the proxy.
func NewPolicy(hops int, networks []netip.Prefix) (Policy, error) {
	if hops < 0 || hops > MaxHops {
		return Policy{}, fmt.Errorf("%w: %d is outside 0..%d", ErrHops, hops, MaxHops)
	}
	for _, network := range networks {
		if !network.IsValid() {
			return Policy{}, fmt.Errorf("%w: an entry is not a valid prefix", ErrNetworks)
		}
		if network.Addr() != network.Masked().Addr() {
			return Policy{}, fmt.Errorf("%w: %s has bits set below its prefix length", ErrNetworks, network)
		}
	}
	if hops == 0 && len(networks) > 0 {
		return Policy{}, fmt.Errorf("%w: networks are declared but no proxy is, so no forwarded header would be read", ErrNetworks)
	}
	return Policy{hops: hops, networks: slices.Clone(networks)}, nil
}

// ParseNetworks reads a comma-separated CIDR list, as configuration supplies
// it. A bare address is accepted and read as a single-host prefix, because
// "10.0.0.7" is what an operator means when there is one proxy.
func ParseNetworks(value string) ([]netip.Prefix, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	entries := strings.Split(trimmed, ",")
	networks := make([]netip.Prefix, 0, len(entries))
	for i, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("%w: entry %d is empty", ErrNetworks, i+1)
		}
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("%w: entry %d is not a CIDR block", ErrNetworks, i+1)
			}
			// A prefix with host bits set is almost always a typo for the
			// block the operator meant. Masking it silently would accept
			// "10.1.2.3/8" as the whole of 10.0.0.0/8 without saying so.
			if prefix.Addr() != prefix.Masked().Addr() {
				return nil, fmt.Errorf("%w: entry %d (%s) has bits set below its prefix length; write %s to mean that block", ErrNetworks, i+1, prefix, prefix.Masked())
			}
			networks = append(networks, prefix)
			continue
		}
		address, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("%w: entry %d is neither an address nor a CIDR block", ErrNetworks, i+1)
		}
		networks = append(networks, netip.PrefixFrom(address, address.BitLen()))
	}
	return networks, nil
}

// Hops returns the declared number of trusted proxies.
func (p Policy) Hops() int { return p.hops }

// Networks returns the peers permitted to be a trusted proxy.
func (p Policy) Networks() []netip.Prefix { return slices.Clone(p.networks) }

// TrustsForwardedHeaders reports whether any forwarded header can be read under
// this policy.
func (p Policy) TrustsForwardedHeaders() bool { return p.hops > 0 }

// Request is the resolved edge view of one request.
type Request struct {
	// ClientIP is the address to attribute the request to. It is always
	// valid: when nothing can be trusted, it is the connection's own peer.
	ClientIP netip.Addr
	// Scheme is "https" or "http".
	Scheme string
	// Host is the host the client addressed.
	Host string
	// Trusted reports whether forwarded headers were honored. It is false
	// when the policy trusts none, when the peer is not a permitted proxy,
	// and when the header was too short for the declared chain — the last of
	// which is a misconfiguration worth logging.
	Trusted bool
}

// Resolve computes the edge view of r.
func (p Policy) Resolve(r *http.Request) Request {
	direct := Request{
		ClientIP: peerAddress(r.RemoteAddr),
		Scheme:   directScheme(r),
		Host:     r.Host,
	}
	if p.hops == 0 || !p.permitsPeer(direct.ClientIP) {
		return direct
	}

	client, ok := p.nthFromRight(r.Header.Values(HeaderFor), parseForwardedAddress)
	if !ok {
		// The chain is shorter than declared. Something is misconfigured;
		// inventing an address from the entries that are there would be
		// guessing, and the connection's own peer is the one fact available.
		return direct
	}
	resolved := Request{ClientIP: client, Scheme: direct.Scheme, Host: direct.Host, Trusted: true}
	if scheme, ok := p.nthFromRightString(r.Header.Values(HeaderProto), parseScheme); ok {
		resolved.Scheme = scheme
	}
	if host, ok := p.nthFromRightString(r.Header.Values(HeaderHost), parseHost); ok {
		resolved.Host = host
	}
	return resolved
}

// permitsPeer reports whether the immediate peer may act as a trusted proxy.
func (p Policy) permitsPeer(peer netip.Addr) bool {
	if len(p.networks) == 0 {
		return true
	}
	if !peer.IsValid() {
		return false
	}
	unmapped := peer.Unmap()
	for _, network := range p.networks {
		if network.Contains(unmapped) {
			return true
		}
	}
	return false
}

// nthFromRight returns the entry the declared hop count points at.
//
// Values are the header's own repetitions, each of which may itself be a
// comma-separated list; HTTP treats the two forms as identical, so they are
// flattened before counting. Counting from the right is the whole safety
// property: the last p.hops entries are the ones this deployment's proxies
// wrote, and the entry just before them is the address the outermost trusted
// proxy actually received the request from.
func nthFromRightOf[T any](values []string, hops int, parse func(string) (T, bool)) (T, bool) {
	var zero T
	entries := make([]string, 0, MaxForwardedEntries)
	for _, value := range values {
		if len(value) > MaxHeaderBytes {
			return zero, false
		}
		for _, entry := range strings.Split(value, ",") {
			if len(entries) == MaxForwardedEntries {
				return zero, false
			}
			entries = append(entries, strings.TrimSpace(entry))
		}
	}
	index := len(entries) - hops
	if index < 0 || index >= len(entries) {
		return zero, false
	}
	return parse(entries[index])
}

func (p Policy) nthFromRight(values []string, parse func(string) (netip.Addr, bool)) (netip.Addr, bool) {
	return nthFromRightOf(values, p.hops, parse)
}

// nthFromRight for string-valued headers.
func (p Policy) nthFromRightString(values []string, parse func(string) (string, bool)) (string, bool) {
	return nthFromRightOf(values, p.hops, parse)
}

// parseForwardedAddress reads one X-Forwarded-For entry.
//
// The entry may carry a port (some proxies add one) and may be an IPv6 address
// in brackets. An unparseable entry is refused rather than skipped: skipping
// would let a client shift the count by inserting garbage.
func parseForwardedAddress(entry string) (netip.Addr, bool) {
	if entry == "" || len(entry) > MaxHostBytes {
		return netip.Addr{}, false
	}
	if address, err := netip.ParseAddr(entry); err == nil {
		return address.Unmap(), true
	}
	if addrPort, err := netip.ParseAddrPort(entry); err == nil {
		return addrPort.Addr().Unmap(), true
	}
	if host, _, err := net.SplitHostPort(entry); err == nil {
		if address, err := netip.ParseAddr(host); err == nil {
			return address.Unmap(), true
		}
	}
	return netip.Addr{}, false
}

func parseScheme(entry string) (string, bool) {
	switch strings.ToLower(entry) {
	case "https":
		return "https", true
	case "http":
		return "http", true
	default:
		return "", false
	}
}

// parseHost accepts only what a Host header may contain, so a forwarded value
// cannot smuggle a delimiter into anything that later builds a URL from it.
func parseHost(entry string) (string, bool) {
	if entry == "" || len(entry) > MaxHostBytes {
		return "", false
	}
	for i := range len(entry) {
		char := entry[i]
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		case char == '.' || char == '-' || char == ':' || char == '[' || char == ']':
		default:
			return "", false
		}
	}
	return entry, true
}

// peerAddress reads the address of the connection itself.
func peerAddress(remoteAddr string) netip.Addr {
	if addrPort, err := netip.ParseAddrPort(remoteAddr); err == nil {
		return addrPort.Addr().Unmap()
	}
	if address, err := netip.ParseAddr(remoteAddr); err == nil {
		return address.Unmap()
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		if address, err := netip.ParseAddr(host); err == nil {
			return address.Unmap()
		}
	}
	return netip.Addr{}
}

func directScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
