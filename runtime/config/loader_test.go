package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoader_String(t *testing.T) {
	t.Run("from environment", func(t *testing.T) {
		t.Setenv("APP_NAME", "nise")
		l := NewLoader()
		got := l.String("APP_NAME", Default("fallback"))
		if got != "nise" {
			t.Fatalf("got %q, want %q", got, "nise")
		}
		if err := l.Err(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("falls back to default", func(t *testing.T) {
		l := NewLoader()
		got := l.String("APP_NAME_UNSET", Default("fallback"))
		if got != "fallback" {
			t.Fatalf("got %q, want %q", got, "fallback")
		}
		if err := l.Err(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("required and missing is an error", func(t *testing.T) {
		l := NewLoader()
		l.String("APP_NAME_STILL_UNSET")
		err := l.Err()
		if err == nil {
			t.Fatal("expected an error for a missing required field")
		}
		if !strings.Contains(err.Error(), "APP_NAME_STILL_UNSET") {
			t.Fatalf("error %q does not name the variable", err)
		}
	})
}

func TestLoader_Int(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		t.Setenv("PORT", "8080")
		l := NewLoader()
		got := l.Int("PORT")
		if got != 8080 {
			t.Fatalf("got %d, want 8080", got)
		}
		if err := l.Err(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Setenv("PORT", "not-a-number")
		l := NewLoader()
		l.Int("PORT")
		err := l.Err()
		if err == nil {
			t.Fatal("expected an error for an invalid integer")
		}
		if !strings.Contains(err.Error(), "PORT") {
			t.Fatalf("error %q does not name the variable", err)
		}
	})
}

func TestLoader_Bool(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		t.Setenv("DEBUG", "true")
		l := NewLoader()
		if !l.Bool("DEBUG") {
			t.Fatal("expected true")
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Setenv("DEBUG", "maybe")
		l := NewLoader()
		l.Bool("DEBUG")
		if err := l.Err(); err == nil {
			t.Fatal("expected an error for an invalid boolean")
		}
	})

	t.Run("default", func(t *testing.T) {
		l := NewLoader()
		if l.Bool("DEBUG_UNSET", Default("false")) {
			t.Fatal("expected false from default")
		}
	})
}

func TestLoader_Duration(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		t.Setenv("TIMEOUT", "30s")
		l := NewLoader()
		got := l.Duration("TIMEOUT")
		if got != 30*time.Second {
			t.Fatalf("got %v, want 30s", got)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Setenv("TIMEOUT", "thirty seconds")
		l := NewLoader()
		l.Duration("TIMEOUT")
		if err := l.Err(); err == nil {
			t.Fatal("expected an error for an invalid duration")
		}
	})
}

func TestLoader_Environment(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		t.Setenv("APP_ENV", "production")
		l := NewLoader()
		got := l.Environment("APP_ENV")
		if got != Production {
			t.Fatalf("got %q, want %q", got, Production)
		}
	})

	t.Run("unrecognized value is an error", func(t *testing.T) {
		t.Setenv("APP_ENV", "staging")
		l := NewLoader()
		l.Environment("APP_ENV")
		err := l.Err()
		if err == nil {
			t.Fatal("expected an error for an unrecognized environment")
		}
		if !strings.Contains(err.Error(), "APP_ENV") {
			t.Fatalf("error %q does not name the variable", err)
		}
	})

	t.Run("default", func(t *testing.T) {
		l := NewLoader()
		got := l.Environment("APP_ENV_UNSET", Default("development"))
		if got != Development {
			t.Fatalf("got %q, want %q", got, Development)
		}
	})
}

// TestLoader_Err_CollectsAllViolations proves that a Loader reports every
// failed field at once rather than stopping at the first.
func TestLoader_Err_CollectsAllViolations(t *testing.T) {
	l := NewLoader()
	l.String("MISSING_STRING")
	l.Int("MISSING_INT")
	l.Bool("MISSING_BOOL")

	err := l.Err()
	if err == nil {
		t.Fatal("expected a combined error")
	}
	for _, name := range []string{"MISSING_STRING", "MISSING_INT", "MISSING_BOOL"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("combined error missing violation for %s: %v", name, err)
		}
	}
}

func TestLoader_Err_IsJoinedError(t *testing.T) {
	l := NewLoader()
	l.String("A")
	l.String("B")
	err := l.Err()
	if err == nil {
		t.Fatal("expected an error")
	}
	// errors.Join produces an error whose Error() contains every joined
	// message separated by newlines; confirm both are present independently
	// rather than relying on ordering.
	if !strings.Contains(err.Error(), "A:") || !strings.Contains(err.Error(), "B:") {
		t.Fatalf("joined error missing a violation: %v", err)
	}
}
