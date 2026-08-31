package pagination_test

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/pagination"
)

func testLimits(t *testing.T) pagination.Limits {
	t.Helper()
	limits, err := pagination.NewLimits(25, 100)
	if err != nil {
		t.Fatalf("NewLimits: %v", err)
	}
	return limits
}

func TestNewLimits(t *testing.T) {
	t.Parallel()

	limits := testLimits(t)
	if limits.Default() != 25 || limits.Max() != 100 {
		t.Fatalf("limits = %d/%d, want 25/100", limits.Default(), limits.Max())
	}

	tests := []struct {
		name       string
		defaultSiz int
		maxSize    int
	}{
		{name: "zero default", defaultSiz: 0, maxSize: 10},
		{name: "negative default", defaultSiz: -1, maxSize: 10},
		{name: "zero maximum", defaultSiz: 10, maxSize: 0},
		{name: "default above maximum", defaultSiz: 20, maxSize: 10},
		{name: "maximum above the package ceiling", defaultSiz: 10, maxSize: pagination.MaxPageLimit + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := pagination.NewLimits(tc.defaultSiz, tc.maxSize); !errors.Is(err, pagination.ErrLimits) {
				t.Fatalf("NewLimits(%d, %d): error = %v, want ErrLimits", tc.defaultSiz, tc.maxSize, err)
			}
		})
	}
}

func TestParsePageFirstPage(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	page, err := codec.ParsePage(testBinding(), url.Values{}, testLimits(t))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if page.HasCursor {
		t.Error("first page reports a cursor")
	}
	if page.Direction != pagination.Forward {
		t.Errorf("Direction = %q, want forward", page.Direction)
	}
	if page.Limit != 25 {
		t.Errorf("Limit = %d, want the configured default 25", page.Limit)
	}
}

func TestParsePageLimits(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	binding := testBinding()
	limits := testLimits(t)

	accepted := map[string]int{"1": 1, "7": 7, "100": 100}
	for raw, want := range accepted {
		t.Run("accepts "+raw, func(t *testing.T) {
			t.Parallel()
			page, err := codec.ParsePage(binding, url.Values{"limit": {raw}}, limits)
			if err != nil {
				t.Fatalf("ParsePage(limit=%s): %v", raw, err)
			}
			if page.Limit != want {
				t.Errorf("Limit = %d, want %d", page.Limit, want)
			}
		})
	}

	rejected := []string{"0", "-1", "101", "+5", "05", " 7", "7 ", "1e2", "0x10", "٧", "99999"}
	for _, raw := range rejected {
		t.Run("rejects "+strings.ReplaceAll(raw, " ", "_"), func(t *testing.T) {
			t.Parallel()
			_, err := codec.ParsePage(binding, url.Values{"limit": {raw}}, limits)
			if !errors.Is(err, pagination.ErrInvalidLimit) {
				t.Fatalf("ParsePage(limit=%q): error = %v, want ErrInvalidLimit", raw, err)
			}
		})
	}

	t.Run("present but empty", func(t *testing.T) {
		t.Parallel()
		for _, name := range []string{"limit", "after", "before"} {
			_, err := codec.ParsePage(binding, url.Values{name: {""}}, limits)
			if !errors.Is(err, pagination.ErrEmptyParameter) {
				t.Errorf("ParsePage(%s=): error = %v, want ErrEmptyParameter", name, err)
			}
		}
	})

	t.Run("repeated limit", func(t *testing.T) {
		t.Parallel()
		_, err := codec.ParsePage(binding, url.Values{"limit": {"5", "10"}}, limits)
		if !errors.Is(err, pagination.ErrRepeatedParameter) {
			t.Fatalf("error = %v, want ErrRepeatedParameter", err)
		}
	})
}

func TestParsePageDirection(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	binding := testBinding()
	limits := testLimits(t)

	next, err := codec.Encode(binding, pagination.Forward, []string{"41812"})
	if err != nil {
		t.Fatalf("Encode forward: %v", err)
	}
	previous, err := codec.Encode(binding, pagination.Backward, []string{"41780"})
	if err != nil {
		t.Fatalf("Encode backward: %v", err)
	}

	forward, err := codec.ParsePage(binding, url.Values{"after": {next}}, limits)
	if err != nil {
		t.Fatalf("ParsePage(after): %v", err)
	}
	if !forward.HasCursor || forward.Direction != pagination.Forward || forward.Cursor.Values[0] != "41812" {
		t.Errorf("forward page = %#v", forward)
	}

	backward, err := codec.ParsePage(binding, url.Values{"before": {previous}}, limits)
	if err != nil {
		t.Fatalf("ParsePage(before): %v", err)
	}
	if !backward.HasCursor || backward.Direction != pagination.Backward || backward.Cursor.Values[0] != "41780" {
		t.Errorf("backward page = %#v", backward)
	}

	t.Run("a forward cursor cannot be replayed as before", func(t *testing.T) {
		t.Parallel()
		_, err := codec.ParsePage(binding, url.Values{"before": {next}}, limits)
		if !errors.Is(err, pagination.ErrCursorDirection) {
			t.Fatalf("error = %v, want ErrCursorDirection", err)
		}
	})
	t.Run("a backward cursor cannot be replayed as after", func(t *testing.T) {
		t.Parallel()
		_, err := codec.ParsePage(binding, url.Values{"after": {previous}}, limits)
		if !errors.Is(err, pagination.ErrCursorDirection) {
			t.Fatalf("error = %v, want ErrCursorDirection", err)
		}
	})
	t.Run("after and before cannot be combined", func(t *testing.T) {
		t.Parallel()
		_, err := codec.ParsePage(binding, url.Values{"after": {next}, "before": {previous}}, limits)
		if !errors.Is(err, pagination.ErrConflictingCursors) {
			t.Fatalf("error = %v, want ErrConflictingCursors", err)
		}
	})
	for _, name := range []string{"after", "before"} {
		t.Run("repeated "+name, func(t *testing.T) {
			t.Parallel()
			_, err := codec.ParsePage(binding, url.Values{name: {next, next}}, limits)
			if !errors.Is(err, pagination.ErrRepeatedParameter) {
				t.Fatalf("error = %v, want ErrRepeatedParameter", err)
			}
		})
	}
}

func TestParsePagePropagatesCursorFailures(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	binding := testBinding()
	limits := testLimits(t)
	token, err := codec.Encode(binding, pagination.Forward, []string{"1"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	tests := []struct {
		name    string
		binding pagination.Binding
		query   url.Values
		want    error
	}{
		{
			name:    "tampered cursor",
			binding: binding,
			query:   url.Values{"after": {token + "x"}},
			want:    pagination.ErrCursorSignature,
		},
		{
			name:    "cursor from another query",
			binding: pagination.NewBinding("/invoices", url.Values{"status": {"paid"}}),
			query:   url.Values{"after": {token}},
			want:    pagination.ErrCursorBinding,
		},
		{
			name:    "not a cursor",
			binding: binding,
			query:   url.Values{"after": {"../../etc/passwd"}},
			want:    pagination.ErrCursorMalformed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := codec.ParsePage(tc.binding, tc.query, limits); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("expired cursor", func(t *testing.T) {
		t.Parallel()
		later := testCodec(t, time.Hour)
		expired, err := later.Encode(binding, pagination.Forward, []string{"1"})
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		ring, err := pagination.NewKeyRing(testKey(t, "active"))
		if err != nil {
			t.Fatalf("NewKeyRing: %v", err)
		}
		aged, err := pagination.NewCodec(ring, time.Hour, pagination.WithClock(func() time.Time {
			return fixedNow.Add(2 * time.Hour)
		}))
		if err != nil {
			t.Fatalf("NewCodec: %v", err)
		}
		if _, err := aged.ParsePage(binding, url.Values{"after": {expired}}, limits); !errors.Is(err, pagination.ErrCursorExpired) {
			t.Fatalf("error = %v, want ErrCursorExpired", err)
		}
	})
}

func TestParsePageRequiresConstructedInputs(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	if _, err := codec.ParsePage(pagination.Binding{}, url.Values{}, testLimits(t)); !errors.Is(err, pagination.ErrBinding) {
		t.Errorf("zero binding: error = %v, want ErrBinding", err)
	}
	if _, err := codec.ParsePage(testBinding(), url.Values{}, pagination.Limits{}); !errors.Is(err, pagination.ErrLimits) {
		t.Errorf("zero limits: error = %v, want ErrLimits", err)
	}
}

func TestParameterNamesAreTheDocumentedOnes(t *testing.T) {
	t.Parallel()

	if pagination.LimitParam != "limit" || pagination.AfterParam != "after" || pagination.BeforeParam != "before" {
		t.Fatalf("parameter names = %q/%q/%q", pagination.LimitParam, pagination.AfterParam, pagination.BeforeParam)
	}
}
