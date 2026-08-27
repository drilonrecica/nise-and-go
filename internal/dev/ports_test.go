package dev

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestValidatePort(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"1", "80", "8080", "65535", " 5173 "} {
		if err := ValidatePort("proxy", ok); err != nil {
			t.Fatalf("ValidatePort(%q) = %v, want nil", ok, err)
		}
	}
	// Port 0 is rejected along with the nonsense: it means "pick
	// anything", and a URL that moves every run is not worth printing.
	for _, bad := range []string{"0", "-1", "65536", "http", "", "80.5"} {
		err := ValidatePort("proxy", bad)
		if err == nil {
			t.Fatalf("ValidatePort(%q) = nil, want an error", bad)
		}
		if !strings.Contains(err.Error(), "proxy port") {
			t.Fatalf("error %v does not name the role", err)
		}
	}
}

func TestCheckPortFreeAcceptsAFreePortAndRejectsAHeldOne(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer func() { _ = ln.Close() }()
	_, held, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("splitting the address: %v", err)
	}

	if err := CheckPortFree("app", "127.0.0.1", held); err == nil {
		t.Fatal("CheckPortFree accepted a port that is currently listening")
	} else {
		var inUse *PortInUseError
		if !errors.As(err, &inUse) {
			t.Fatalf("error %v is not a *PortInUseError", err)
		}
		if inUse.Role != "app" || !strings.Contains(inUse.Addr, held) {
			t.Fatalf("PortInUseError = %+v, want it to name the role and address", inUse)
		}
		recovery := inUse.Recovery("--app-port")
		if !strings.Contains(recovery, "--app-port") || !strings.Contains(recovery, held) {
			t.Fatalf("Recovery() = %q, want it to name the flag and a command mentioning the port", recovery)
		}
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	if err := CheckPortFree("app", "127.0.0.1", held); err != nil {
		t.Fatalf("CheckPortFree(%s) = %v after the listener closed, want nil", held, err)
	}
}

func TestPortHolderCommandNamesThePort(t *testing.T) {
	t.Parallel()
	got := portHolderCommand("127.0.0.1:8080")
	if !strings.Contains(got, "8080") {
		t.Fatalf("portHolderCommand = %q, want it to name the port", got)
	}
	if got := portHolderCommand("8080"); !strings.Contains(got, "8080") {
		t.Fatalf("portHolderCommand on a bare port = %q, want it to name the port", got)
	}
}

func TestDefaultPortsDoNotCollide(t *testing.T) {
	t.Parallel()
	seen := map[string]string{}
	for role, port := range map[string]string{
		"proxy": DefaultProxyPort,
		"app":   DefaultAppPort,
		"vite":  DefaultVitePort,
		"db":    DefaultDatabasePort,
	} {
		if other, ok := seen[port]; ok {
			t.Fatalf("the %s and %s defaults are both %s", role, other, port)
		}
		seen[port] = role
		if err := ValidatePort(role, port); err != nil {
			t.Fatalf("the %s default is not a valid port: %v", role, err)
		}
	}
	// The proxy is what a developer opens, so it must match the port the
	// application binds in production (deploy/compose.yaml publishes 8080).
	if DefaultProxyPort != "8080" {
		t.Fatalf("DefaultProxyPort = %q, want 8080 so the dev URL matches the production one", DefaultProxyPort)
	}
}
