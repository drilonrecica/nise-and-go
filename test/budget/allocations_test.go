package budget_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/authz"
	"github.com/drilonrecica/nise-and-go/runtime/logging"
	"github.com/drilonrecica/nise-and-go/runtime/pagination"
	"github.com/drilonrecica/nise-and-go/runtime/session"
)

// Allocation budgets for the paths every request touches.
//
// Allocations rather than time, because allocations are a property of the
// code and time is a property of the machine. `testing.AllocsPerRun` returns
// the same number on a laptop and on a shared runner, so a change here is
// always a change somebody made.
//
// Each budget is the measured count plus a small allowance, and the allowance
// is not there to absorb regressions — it is there so that a Go release which
// shifts one allocation in the standard library does not fail the build on the
// day it lands. A change that needs the budget raised should raise it in the
// same commit, with the reason.
const (
	// budgetHeadroom is added to every measured figure below.
	budgetHeadroom = 2
)

// assertAllocations fails when fn allocates more than budget times per call.
func assertAllocations(t *testing.T, name string, budget int, fn func()) {
	t.Helper()

	// Warm anything lazily initialised, so the first call's one-time cost is
	// not attributed to every call.
	fn()

	average := testing.AllocsPerRun(200, fn)
	actual := int(average)
	if actual > budget {
		t.Errorf("%s allocates %d times per call, budget is %d.\n"+
			"If this increase is intended, raise the budget in the same commit and say why.", name, actual, budget)
	}
	// A budget far above what is measured stops being a gate. This reports
	// rather than fails, because tightening it is somebody's deliberate
	// choice and not something a test should force mid-change.
	if budget-actual > 8 {
		t.Logf("%s allocates %d times per call against a budget of %d; consider tightening it", name, actual, budget)
	}
}

// TestPermissionCheckAllocations covers the check every authorized request
// makes, often several times.
func TestPermissionCheckAllocations(t *testing.T) {
	read := authz.MustPermission("users.read")
	write := authz.MustPermission("users.manage")
	role, err := authz.NewRole("administrator", read, write)
	if err != nil {
		t.Fatalf("NewRole: %v", err)
	}
	held := role.Permissions()

	assertAllocations(t, "authz.Set.Has", budgetHeadroom, func() {
		if !held.Has(read) {
			t.Fatal("Has returned false for a held permission")
		}
	})
	assertAllocations(t, "authz.Set.HasAll", budgetHeadroom, func() {
		if !held.HasAll(read, write) {
			t.Fatal("HasAll returned false for held permissions")
		}
	})
}

// TestSessionTokenParseAllocations covers the parse every authenticated
// request performs exactly once, on a value that arrived from a cookie.
func TestSessionTokenParseAllocations(t *testing.T) {
	token, err := session.New()
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	raw := token.Value()

	assertAllocations(t, "session.Parse", 4+budgetHeadroom, func() {
		if _, err := session.Parse(raw); err != nil {
			t.Fatalf("Parse: %v", err)
		}
	})
}

// TestCursorAllocations covers pagination, which every list endpoint reaches
// twice — once to verify the cursor that arrived and once to issue the next.
func TestCursorAllocations(t *testing.T) {
	key, err := pagination.NewKey("budget", bytes.Repeat([]byte{0x2a}, 32))
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	ring, err := pagination.NewKeyRing(key)
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	codec, err := pagination.NewCodec(ring, time.Hour)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	binding := pagination.NewBinding("widgets", url.Values{"status": {"open"}})

	assertAllocations(t, "pagination.NewBinding", 11+budgetHeadroom, func() {
		_ = pagination.NewBinding("widgets", url.Values{"status": {"open"}})
	})

	values := []string{"2026-09-01T00:00:00Z", "01J8Z0000000000000000000"}
	encoded, err := codec.Encode(binding, pagination.Forward, values)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	assertAllocations(t, "pagination.Codec.Encode", 18+budgetHeadroom, func() {
		if _, err := codec.Encode(binding, pagination.Forward, values); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	})
	assertAllocations(t, "pagination.Codec.Decode", 32+budgetHeadroom, func() {
		if _, err := codec.Decode(binding, encoded); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	})
}

// TestRedactionAllocations covers what every log line pays.
//
// Redaction runs on the hot path of a request that is already doing real
// work, so a regression here is paid on every line rather than once.
func TestRedactionAllocations(t *testing.T) {
	var sink bytes.Buffer
	logger := slog.New(logging.NewJSONHandler(&sink, logging.HandlerOptions{Level: slog.LevelInfo}))

	assertAllocations(t, "logging redaction", 24+budgetHeadroom, func() {
		sink.Reset()
		logger.LogAttrs(context.Background(), slog.LevelInfo, "request completed",
			slog.String("method", "GET"),
			slog.String("path", "/api/v1/widgets"),
			slog.String("authorization", "Bearer secret-value"),
			slog.Int("status", 200))
	})
}
