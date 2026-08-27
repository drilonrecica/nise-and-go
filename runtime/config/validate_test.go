package config

import (
	"strings"
	"testing"
)

func TestValidate_SkipsNonProduction(t *testing.T) {
	called := false
	for _, env := range []Environment{Development, Test} {
		err := Validate(env, func(v *Validator) {
			called = true
			v.Check(false, "ANYTHING", "always fails", "never runs")
		})
		if err != nil {
			t.Fatalf("Validate(%q, ...) = %v, want nil", env, err)
		}
	}
	if called {
		t.Fatal("checks ran for a non-production environment")
	}
}

func TestValidate_RunsInProduction(t *testing.T) {
	err := Validate(Production, func(v *Validator) {
		v.Check(true, "OK", "unused", "unused")
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidate_CollectsAllViolations proves that production validation
// reports every violation at once, not just the first one found.
func TestValidate_CollectsAllViolations(t *testing.T) {
	dbPassword := Secret{} // unset

	err := Validate(Production, func(v *Validator) {
		v.Check(false, "DEBUG", "debug mode is enabled in production", "set DEBUG=false")
		v.Check(false, "COOKIE_SECURE", "insecure cookie settings are enabled in production", "set COOKIE_SECURE=true")
		v.Check(false, "BIND_ADDR", "bound to a public address without an explicit opt-in", "set BIND_ADDR to a private address or ALLOW_PUBLIC_BIND=true")
		v.RequireSecretSet("DB_PASSWORD", dbPassword)
	})

	if err == nil {
		t.Fatal("expected a combined validation error")
	}

	for _, name := range []string{"DEBUG", "COOKIE_SECURE", "BIND_ADDR", "DB_PASSWORD"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("combined error missing violation for %s: %v", name, err)
		}
	}
}

func TestValidate_NoViolationsIsNil(t *testing.T) {
	err := Validate(Production, func(v *Validator) {
		v.Check(true, "A", "unused", "unused")
		v.RequireSecretSet("B", NewSecret("set"))
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidator_RequireSecretSet_NeverIncludesValue(t *testing.T) {
	v := &Validator{}
	v.RequireSecretSet("API_KEY", NewSecret(""))
	err := v.Err()
	if err == nil {
		t.Fatal("expected a violation for an unset secret")
	}
	if !strings.Contains(err.Error(), "API_KEY") {
		t.Fatalf("error %q does not name the variable", err)
	}
}

func TestValidator_CheckMessageShape(t *testing.T) {
	v := &Validator{}
	v.Check(false, "SOME_VAR", "is wrong", "do this")
	err := v.Err()
	if err == nil {
		t.Fatal("expected an error")
	}
	got := err.Error()
	for _, want := range []string{"SOME_VAR", "is wrong", "do this"} {
		if !strings.Contains(got, want) {
			t.Errorf("message %q missing %q", got, want)
		}
	}
}
