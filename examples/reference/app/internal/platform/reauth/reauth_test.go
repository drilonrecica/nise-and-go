// Generated once by nise; owned by this application. Nise will not overwrite it.

package reauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/session"
)

func TestDefaultMatrixDeclaresTheDocumentedActions(t *testing.T) {
	t.Parallel()

	matrix, err := DefaultMatrix()
	if err != nil {
		t.Fatalf("DefaultMatrix() = %v, want a matrix", err)
	}

	want := map[Action]time.Duration{
		ActionRolesGrant:        WindowEscalation,
		ActionUsersEnable:       WindowAdministrative,
		ActionInvitationsCreate: WindowAdministrative,
		// The optional TOTP module adds its own entry, on the same
		// principle: removing a second factor widens what a stolen session
		// can do.
		ActionSecondFactorDisable: WindowEscalation,
		// Workbench's own entry, and the only one that is not about access.
		// It earns its place for the same reason the others do: retiring a
		// resource takes something away from everybody at once, and there is
		// no undo that gives somebody their Thursday slot back.
		ActionResourcesRetire: WindowAdministrative,
	}
	actions := matrix.Actions()
	if len(actions) != len(want) {
		t.Fatalf("the matrix declares %v, want %d actions", actions, len(want))
	}
	for action, window := range want {
		freshness, declared := matrix.Freshness(action)
		if !declared {
			t.Fatalf("action %s is not declared", action)
		}
		if freshness.Window() != window {
			t.Fatalf("action %s has window %s, want %s", action, freshness.Window(), window)
		}
	}

	// Actions returns a stable order, so a startup log line and a diff of it
	// mean something.
	for i := 1; i < len(actions); i++ {
		if actions[i-1] >= actions[i] {
			t.Fatalf("Actions() is not sorted: %v", actions)
		}
	}
}

// Withdrawing access is deliberately not in the matrix: the withdrawal is the
// response to a compromise, and making the response slower than the attack
// would help the wrong party. Changing a password is not in it either, because
// that action takes the current password and is a proof in its own right.
func TestWithdrawalAndPasswordChangeAreNotInTheMatrix(t *testing.T) {
	t.Parallel()

	matrix, err := DefaultMatrix()
	if err != nil {
		t.Fatalf("DefaultMatrix() = %v, want a matrix", err)
	}
	// resources.manage is in this list on purpose: editing a resource's
	// description inconveniences nobody, and a matrix that covers everything
	// is one somebody turns off.
	for _, action := range []Action{
		"roles.revoke", "users.disable", "sessions.revoke", "password.change", "audit.read",
		"resources.manage", "reservations.create", "reservations.cancel",
	} {
		if _, declared := matrix.Freshness(action); declared {
			t.Fatalf("action %s is declared; the matrix is supposed to be short", action)
		}
	}
}

func TestNewMatrixRefusesAnUnusableDeclaration(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		entries []Entry
	}{
		{
			name: "an action with no dot",
			entries: []Entry{
				{Action: "grant", Window: WindowEscalation},
			},
		},
		{
			name: "an action with a wildcard",
			entries: []Entry{
				{Action: "roles.*", Window: WindowEscalation},
			},
		},
		{
			name: "an uppercase action",
			entries: []Entry{
				{Action: "Roles.Grant", Window: WindowEscalation},
			},
		},
		{
			name: "the same action twice",
			entries: []Entry{
				{Action: ActionRolesGrant, Window: WindowEscalation},
				{Action: ActionRolesGrant, Window: WindowAdministrative},
			},
		},
		{
			name: "a window of zero",
			entries: []Entry{
				{Action: ActionRolesGrant},
			},
		},
		{
			name: "a window longer than a day",
			entries: []Entry{
				{Action: ActionRolesGrant, Window: 48 * time.Hour},
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewMatrix(testCase.entries...); err == nil {
				t.Fatal("NewMatrix accepted an unusable declaration")
			}
		})
	}
}

// A matrix that was never constructed declares nothing, and declaring nothing
// denies. The alternative — a nil matrix that allows — would turn a wiring
// mistake into an application with no reauthentication at all.
func TestTheNilMatrixDeclaresNothing(t *testing.T) {
	t.Parallel()

	var matrix *Matrix
	if _, declared := matrix.Freshness(ActionRolesGrant); declared {
		t.Fatal("a nil matrix declared an action")
	}
	if actions := matrix.Actions(); len(actions) != 0 {
		t.Fatalf("a nil matrix returned %v", actions)
	}
}

func TestRequireDeniesInEveryDirectionItCanFail(t *testing.T) {
	t.Parallel()

	matrix, err := DefaultMatrix()
	if err != nil {
		t.Fatalf("DefaultMatrix() = %v, want a matrix", err)
	}
	now := time.Now().UTC()

	for _, testCase := range []struct {
		name    string
		state   *State
		action  Action
		wantErr bool
		reason  string
	}{
		{
			name:    "the middleware did not run",
			action:  ActionRolesGrant,
			wantErr: true,
			reason:  ReasonUnresolved,
		},
		{
			// A position built by hand, with no instant to judge against,
			// is not a resolved position.
			name:    "the position carries no instant",
			state:   &State{Matrix: matrix, UserID: "a4c7f2d0-0000-4000-8000-000000000001", ProvenAt: now},
			action:  ActionRolesGrant,
			wantErr: true,
			reason:  ReasonUnresolved,
		},
		{
			name:    "nobody is signed in",
			state:   &State{At: now, Matrix: matrix},
			action:  ActionRolesGrant,
			wantErr: true,
			reason:  ReasonNoSession,
		},
		{
			name:    "the action is not declared",
			state:   &State{At: now, Matrix: matrix, UserID: "a4c7f2d0-0000-4000-8000-000000000001", ProvenAt: now},
			action:  "roles.revoke",
			wantErr: true,
			reason:  ReasonUndeclared,
		},
		{
			name:    "the proof is older than the window",
			state:   &State{At: now, Matrix: matrix, UserID: "a4c7f2d0-0000-4000-8000-000000000001", ProvenAt: now.Add(-WindowEscalation - time.Second)},
			action:  ActionRolesGrant,
			wantErr: true,
			reason:  ReasonStale,
		},
		{
			name:    "no proof was ever recorded",
			state:   &State{At: now, Matrix: matrix, UserID: "a4c7f2d0-0000-4000-8000-000000000001"},
			action:  ActionRolesGrant,
			wantErr: true,
			reason:  ReasonStale,
		},
		{
			name:   "the proof is inside the window",
			state:  &State{At: now, Matrix: matrix, UserID: "a4c7f2d0-0000-4000-8000-000000000001", ProvenAt: now.Add(-time.Minute)},
			action: ActionRolesGrant,
		},
		{
			// A window long enough for one action is not long enough for
			// another. The matrix is per action, not per session.
			name:    "a proof good for an administrative action is not good for an escalation",
			state:   &State{At: now, Matrix: matrix, UserID: "a4c7f2d0-0000-4000-8000-000000000001", ProvenAt: now.Add(-10 * time.Minute)},
			action:  ActionRolesGrant,
			wantErr: true,
			reason:  ReasonStale,
		},
		{
			name:   "and that same proof is good for the administrative action",
			state:  &State{At: now, Matrix: matrix, UserID: "a4c7f2d0-0000-4000-8000-000000000001", ProvenAt: now.Add(-10 * time.Minute)},
			action: ActionUsersEnable,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			if testCase.state != nil {
				ctx = WithState(ctx, *testCase.state)
			}
			err := Require(ctx, testCase.action)
			if !testCase.wantErr {
				if err != nil {
					t.Fatalf("Require(%s) = %v, want nil", testCase.action, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Require(%s) allowed the action", testCase.action)
			}
			if !errors.Is(err, ErrRequired) {
				t.Fatalf("error %v does not match ErrRequired", err)
			}
			var required *RequiredError
			if !errors.As(err, &required) {
				t.Fatalf("error %v is not a RequiredError", err)
			}
			if required.Reason != testCase.reason {
				t.Fatalf("reason = %q, want %q", required.Reason, testCase.reason)
			}
			if required.Action != testCase.action {
				t.Fatalf("action = %q, want %q", required.Action, testCase.action)
			}
		})
	}
}

// The demand carries the window so a caller can tell somebody how recent a
// proof has to be, which is the one thing about this that is useful to say out
// loud.
func TestAStaleDemandCarriesTheWindow(t *testing.T) {
	t.Parallel()

	matrix, err := DefaultMatrix()
	if err != nil {
		t.Fatalf("DefaultMatrix() = %v, want a matrix", err)
	}
	provenAt := time.Now().UTC().Add(-time.Hour)
	ctx := WithState(context.Background(), State{
		At:       time.Now().UTC(),
		Matrix:   matrix,
		UserID:   "a4c7f2d0-0000-4000-8000-000000000001",
		ProvenAt: provenAt,
	})

	var required *RequiredError
	if !errors.As(Require(ctx, ActionRolesGrant), &required) {
		t.Fatal("a stale proof did not produce a RequiredError")
	}
	if required.Window != WindowEscalation {
		t.Fatalf("window = %s, want %s", required.Window, WindowEscalation)
	}
	if !required.ProvenAt.Equal(provenAt) {
		t.Fatalf("provenAt = %s, want %s", required.ProvenAt, provenAt)
	}
	if required.UserID == "" {
		t.Fatal("the demand does not name the caller")
	}
}

// The instant is captured once per request, so two checks in one request
// cannot disagree about whether a proof was still fresh.
func TestOneRequestHasOneInstant(t *testing.T) {
	t.Parallel()

	matrix, err := DefaultMatrix()
	if err != nil {
		t.Fatalf("DefaultMatrix() = %v, want a matrix", err)
	}
	// A clock that jumps past the escalation window between reads. If Require
	// read it, the second check would refuse what the first allowed.
	instant := time.Now().UTC()
	reads := 0
	clock := func() time.Time {
		reads++
		return instant.Add(time.Duration(reads) * WindowEscalation)
	}
	record := session.Record{
		ID:       "a4c7f2d0-0000-4000-8000-000000000002",
		UserID:   "a4c7f2d0-0000-4000-8000-000000000001",
		ProvenAt: instant.Add(WindowEscalation),
	}
	resolver, err := NewResolver(matrix, func(context.Context) (session.Record, bool) {
		return record, true
	}, WithClock(clock))
	if err != nil {
		t.Fatalf("NewResolver = %v, want a resolver", err)
	}

	var first, second error
	handler := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		first = Require(r.Context(), ActionRolesGrant)
		second = Require(r.Context(), ActionRolesGrant)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/roles", nil))

	if first != nil || second != nil {
		t.Fatalf("checks disagreed within one request: first = %v, second = %v", first, second)
	}
	if reads != 1 {
		t.Fatalf("the clock was read %d times in one request, want 1", reads)
	}
}

func TestNewResolverRefusesAnIncompleteWiring(t *testing.T) {
	t.Parallel()

	matrix, err := DefaultMatrix()
	if err != nil {
		t.Fatalf("DefaultMatrix() = %v, want a matrix", err)
	}
	if _, err := NewResolver(nil, func(context.Context) (session.Record, bool) { return session.Record{}, false }); err == nil {
		t.Fatal("NewResolver accepted a missing matrix")
	}
	if _, err := NewResolver(matrix, nil); err == nil {
		t.Fatal("NewResolver accepted a missing session source")
	}
}

func TestMiddlewareResolvesThePositionOnce(t *testing.T) {
	t.Parallel()

	matrix, err := DefaultMatrix()
	if err != nil {
		t.Fatalf("DefaultMatrix() = %v, want a matrix", err)
	}
	provenAt := time.Now().UTC().Add(-time.Minute)
	record := session.Record{
		ID:       "a4c7f2d0-0000-4000-8000-000000000002",
		UserID:   "a4c7f2d0-0000-4000-8000-000000000001",
		ProvenAt: provenAt,
	}

	for _, testCase := range []struct {
		name          string
		authenticated bool
		wantUser      string
		wantAllowed   bool
	}{
		{name: "a signed-in caller", authenticated: true, wantUser: record.UserID, wantAllowed: true},
		{name: "an anonymous caller"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			resolver, err := NewResolver(matrix, func(context.Context) (session.Record, bool) {
				if !testCase.authenticated {
					return session.Record{}, false
				}
				return record, true
			})
			if err != nil {
				t.Fatalf("NewResolver = %v, want a resolver", err)
			}

			var seen State
			var resolved bool
			var allowed bool
			handler := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen, resolved = StateFrom(r.Context())
				allowed = Require(r.Context(), ActionRolesGrant) == nil
			}))

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/roles", nil))

			// The position is attached either way, so "not signed in" and
			// "the middleware is not wired" stay different answers.
			if !resolved {
				t.Fatal("the middleware attached no position")
			}
			if seen.UserID != testCase.wantUser {
				t.Fatalf("UserID = %q, want %q", seen.UserID, testCase.wantUser)
			}
			if seen.At.IsZero() {
				t.Fatal("the position carries no instant to judge proofs against")
			}
			if testCase.authenticated && seen.SessionID != record.ID {
				t.Fatalf("SessionID = %q, want %q", seen.SessionID, record.ID)
			}
			if allowed != testCase.wantAllowed {
				t.Fatalf("Require allowed = %t, want %t", allowed, testCase.wantAllowed)
			}
			if resolver.Matrix() != matrix {
				t.Fatal("the resolver reports a different matrix than it was built with")
			}
		})
	}
}
