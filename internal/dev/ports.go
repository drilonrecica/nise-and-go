package dev

import (
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
)

// The default ports. They are fixed rather than auto-selected: a developer
// bookmarks the dev URL, and a port that moves every run makes the bookmark
// a lie. Every one of them is overridable.
const (
	// DefaultProxyPort is the one port a developer opens. It matches the
	// application's own default PORT and the port deploy/compose.yaml
	// publishes, so the development URL and the production URL differ in
	// nothing but the host.
	DefaultProxyPort = "8080"
	// DefaultAppPort is where the Go child listens, behind the proxy.
	DefaultAppPort = "8081"
	// DefaultVitePort is where Vite listens, behind the proxy. It is
	// Vite's own default, so a developer who runs `pnpm --dir frontend
	// dev` by hand finds it where they expect.
	DefaultVitePort = "5173"
	// DefaultDatabasePort is where the PostgreSQL container is published
	// on loopback.
	DefaultDatabasePort = "5432"
)

// PortInUseError reports that a port `nise dev` needs is already held.
type PortInUseError struct {
	// Role names what nise wanted the port for, e.g. "proxy".
	Role string
	// Addr is the host:port that could not be bound.
	Addr string
	// Err is the underlying listen error.
	Err error
}

// Error implements error.
func (e *PortInUseError) Error() string {
	return fmt.Sprintf("the %s port %s is already in use: %v", e.Role, e.Addr, e.Err)
}

// Unwrap implements errors.Unwrap.
func (e *PortInUseError) Unwrap() error { return e.Err }

// Recovery is the concrete next step, including the command that names the
// holder of the port on this operating system.
//
// There is no portable way to identify a process by the port it holds from
// the standard library — the syscall involved differs on every platform and
// on Linux requires reading /proc — so nise tells the developer the command
// rather than pretending to know the answer.
func (e *PortInUseError) Recovery(flag string) string {
	return fmt.Sprintf("Stop whatever holds it, or pass %s to choose another. Find the holder with: %s",
		flag, portHolderCommand(e.Addr))
}

// portHolderCommand returns the platform's usual "what holds this port"
// command for addr.
func portHolderCommand(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = addr
	}
	switch runtime.GOOS {
	case "windows":
		return `netstat -ano | findstr :` + port
	case "darwin":
		return "lsof -nP -iTCP:" + port + " -sTCP:LISTEN"
	default:
		return "ss -lptn 'sport = :" + port + "'"
	}
}

// CheckPortFree reports whether a TCP port can be bound on host, closing
// the probe listener immediately.
//
// This is a check with an unavoidable race: something else can take the
// port between this call and the child that wants it. It is still worth
// making, because the failure it prevents is the confusing one — three
// children start, one dies silently, and the developer sees a proxy that
// 502s — whereas the race it cannot prevent produces the child's own
// straightforward "address already in use".
func CheckPortFree(role, host, port string) error {
	addr := net.JoinHostPort(host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return &PortInUseError{Role: role, Addr: addr, Err: err}
	}
	if err := ln.Close(); err != nil {
		return fmt.Errorf("releasing the probe listener on %s: %w", addr, err)
	}
	return nil
}

// ValidatePort rejects a port that is not a number in range. Port 0 is
// rejected along with the rest: it means "pick anything", and a dev loop
// that picks a different port every run cannot print a URL worth
// bookmarking.
func ValidatePort(role, port string) error {
	n, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("the %s port must be a number between 1 and 65535, found %q", role, port)
	}
	return nil
}
