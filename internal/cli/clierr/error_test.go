package clierr

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewAndAccessors(t *testing.T) {
	t.Parallel()
	e := New(ExitUsage, "unknown command \"fro\"", "Run \"nise help\".")
	if e.Cause() != "unknown command \"fro\"" {
		t.Errorf("Cause() = %q", e.Cause())
	}
	if e.Recovery() != "Run \"nise help\"." {
		t.Errorf("Recovery() = %q", e.Recovery())
	}
	if e.ExitCode() != ExitUsage {
		t.Errorf("ExitCode() = %v, want ExitUsage", e.ExitCode())
	}
	if e.Code() != "usage_error" {
		t.Errorf("Code() = %q, want usage_error (derived default)", e.Code())
	}
	if e.Docs() != "" {
		t.Errorf("Docs() = %q, want empty", e.Docs())
	}
	if e.Details() != nil {
		t.Errorf("Details() = %v, want nil", e.Details())
	}
	if e.Chain() != nil {
		t.Errorf("Chain() = %v, want nil (no wrapped error)", e.Chain())
	}
}

func TestWithCodeAndDocs(t *testing.T) {
	t.Parallel()
	e := New(ExitPrecondition, "missing tool", "install it").
		WithCode("doctor.missing_tool").
		WithDocs("docs/cli-output.md#usage-errors")
	if e.Code() != "doctor.missing_tool" {
		t.Errorf("Code() = %q", e.Code())
	}
	if e.Docs() != "docs/cli-output.md#usage-errors" {
		t.Errorf("Docs() = %q", e.Docs())
	}
}

func TestWithDetailRedactsSecretShapedKeys(t *testing.T) {
	t.Parallel()
	e := New(ExitError, "cause", "recovery").
		WithDetail("dsn", "postgres://u:p@host/db").
		WithDetail("db_password", "hunter2").
		WithDetail("attempt", "3")

	details := e.Details()
	if details["attempt"] != "3" {
		t.Errorf("details[attempt] = %q, want unredacted 3", details["attempt"])
	}
	if details["db_password"] != RedactPlaceholder {
		t.Errorf("details[db_password] = %q, want %q", details["db_password"], RedactPlaceholder)
	}
	// "dsn" itself is not a deny fragment; this documents that WithDetail
	// only catches key-shaped secrets, not secret-shaped values under an
	// innocuous key. Structured detail keys should be chosen accordingly.
	if details["dsn"] != "postgres://u:p@host/db" {
		t.Errorf("details[dsn] was altered unexpectedly: %q", details["dsn"])
	}
}

func TestDetailsIsACopy(t *testing.T) {
	t.Parallel()
	e := New(ExitError, "c", "r").WithDetail("k", "v")
	d1 := e.Details()
	d1["k"] = "mutated"
	d2 := e.Details()
	if d2["k"] != "v" {
		t.Errorf("Details() leaked a mutable reference: got %q after mutating a prior copy", d2["k"])
	}
}

func TestWrapErrorString(t *testing.T) {
	t.Parallel()
	underlying := errors.New("connection refused")
	e := Wrap(underlying, ExitPrecondition, "could not reach the database", "start postgres and retry")
	want := `could not reach the database: connection refused`
	if e.Error() != want {
		t.Errorf("Error() = %q, want %q", e.Error(), want)
	}
}

func TestNewErrorStringOmitsWrappedPart(t *testing.T) {
	t.Parallel()
	e := New(ExitUsage, "unknown command", "run help")
	if e.Error() != "unknown command" {
		t.Errorf("Error() = %q, want just the cause", e.Error())
	}
}

func TestErrorsIsThroughWrap(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel boom")
	ce := Wrap(sentinel, ExitError, "cause", "recovery")

	if !errors.Is(ce, sentinel) {
		t.Error("errors.Is(ce, sentinel) = false, want true")
	}

	// Wrapping ce again with fmt.Errorf's %w must not break the chain in
	// either direction.
	outer := fmt.Errorf("outer: %w", ce)
	if !errors.Is(outer, sentinel) {
		t.Error("errors.Is(outer, sentinel) = false, want true (double-wrapped)")
	}
}

func TestErrorsAsThroughChain(t *testing.T) {
	t.Parallel()
	ce := New(ExitUsage, "bad flag", "fix it")
	wrapped := fmt.Errorf("dispatch: %w", ce)

	var got *Error
	if !errors.As(wrapped, &got) {
		t.Fatal("errors.As(wrapped, &got) = false, want true")
	}
	if got != ce {
		t.Error("errors.As found a different *Error instance")
	}
}

func TestChainScrubsURLUserinfo(t *testing.T) {
	t.Parallel()
	inner := errors.New("dial postgres://alice:s3cr3t@db.internal:5432/app: connection refused")
	outer := fmt.Errorf("migrate failed: %w", inner)
	ce := Wrap(outer, ExitPrecondition, "could not run migrations", "check DATABASE_URL")

	chain := ce.Chain()
	if len(chain) != 2 {
		t.Fatalf("Chain() has %d lines, want 2: %v", len(chain), chain)
	}
	for _, line := range chain {
		if want := "alice:s3cr3t"; strings.Contains(line, want) {
			t.Errorf("Chain() line %q still contains credentials", line)
		}
	}
	if !strings.Contains(chain[1], RedactPlaceholder) {
		t.Errorf("Chain() innermost line = %q, want it to contain %q", chain[1], RedactPlaceholder)
	}
}

func TestFromPassesThroughExistingError(t *testing.T) {
	t.Parallel()
	ce := New(ExitUsage, "cause", "recovery")
	if From(ce) != ce {
		t.Error("From(ce) returned a different instance for an existing *Error")
	}
}

func TestFromPassesThroughWrappedError(t *testing.T) {
	t.Parallel()
	ce := New(ExitPrecondition, "cause", "recovery")
	wrapped := fmt.Errorf("context: %w", ce)
	if From(wrapped) != ce {
		t.Error("From(wrapped) did not unwrap to the original *Error via errors.As")
	}
}

func TestFromWrapsPlainError(t *testing.T) {
	t.Parallel()
	plain := errors.New("boom")
	got := From(plain)
	if got.ExitCode() != ExitError {
		t.Errorf("From(plain).ExitCode() = %v, want ExitError", got.ExitCode())
	}
	if !errors.Is(got, plain) {
		t.Error("From(plain) lost the original error from its chain")
	}
}

func TestFromNil(t *testing.T) {
	t.Parallel()
	if From(nil) != nil {
		t.Error("From(nil) should return nil")
	}
}

func TestExitCodeValuesAreStable(t *testing.T) {
	t.Parallel()
	// This test exists to fail loudly if anyone changes what an existing
	// exit code number means — it is a public, documented contract
	// (docs/cli-output.md) that scripts depend on.
	cases := map[ExitCode]int{
		ExitOK:           0,
		ExitError:        1,
		ExitUsage:        2,
		ExitPrecondition: 3,
	}
	for code, want := range cases {
		if int(code) != want {
			t.Errorf("exit code %v = %d, want %d", code, int(code), want)
		}
	}
}
