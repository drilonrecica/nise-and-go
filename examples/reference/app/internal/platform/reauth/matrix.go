// Generated once by nise; owned by this application. Nise will not overwrite it.

// Package reauth declares which of this application's actions need a recent
// proof of identity, and how recent.
//
// # What it is for
//
// A session answers "was a credential presented". It does not answer "is that
// person still there". The gap between the two is the unlocked laptop, the
// borrowed phone, and the tab left open on a shared machine. For most requests
// the gap does not matter; for the few that create a new way into the system it
// does, and asking for the password again is the cheapest way to close it.
//
// # The matrix
//
// The declarations here are the reauthentication matrix: the closed list of
// actions that require a proof, each with the age of proof it will accept. Like
// the permission catalog, it is code rather than configuration — the meaning of
// an action is decided by the use case that checks it, so the two have to be
// reviewed together, and every replica must agree.
//
// Adding an action is two edits in one commit: declare it here, and check it in
// the use case. An action checked but not declared is refused rather than
// allowed, so the two cannot drift apart into a check that quietly passes.
//
// # What is deliberately not in it
//
//   - Changing a password. That action already takes the current password,
//     which is a stronger proof than a recent one, and it records a fresh proof
//     on the session that made the change.
//   - Revoking anything: sessions, roles, invitations, an account's access.
//     Withdrawal is the response to a compromise, and a matrix that made the
//     response slower than the attack would be helping the wrong party.
//   - Reading. A matrix that covers everything is one somebody turns off.
//
// The list is short on purpose. Every entry is friction a real person pays
// every time, and friction spent where it buys nothing is what teaches people
// to type their password into whatever asks for it.
package reauth

import (
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/session"
)

// Windows this application uses. They are named rather than repeated so that
// two actions of the same severity cannot drift apart by accident.
const (
	// WindowEscalation is for actions that can lead to every other
	// permission. It is the shortest window a person will tolerate being
	// asked for repeatedly.
	WindowEscalation = 5 * time.Minute
	// WindowAdministrative is for actions that create or restore a way in.
	WindowAdministrative = 15 * time.Minute
)

// Actions this application requires a recent proof for.
const (
	// ActionRolesGrant is granting a role. It is the escalation path: the
	// role granted can carry every permission the granter holds, and some
	// they do not use themselves.
	ActionRolesGrant Action = "roles.grant"
	// ActionUsersEnable is turning a disabled account back on. Disabling is
	// deliberately not here — closing a door quickly is the whole point of
	// having one.
	ActionUsersEnable Action = "users.enable"
	// ActionInvitationsCreate is inviting somebody. An invitation is a way
	// into the system that outlives the request that made it.
	ActionInvitationsCreate Action = "invitations.create"
	// ActionSecondFactorDisable is removing an account's second factor.
	// Adding one narrows what a stolen session can do and asks for nothing;
	// removing one widens it, which is exactly what this matrix is for.
	ActionSecondFactorDisable Action = "second_factor.disable"
	// ActionResourcesRetire is taking a resource out of service.
	//
	// It is the one entry here that is not about access, and it earns its
	// place for the same reason the others do: it takes something away from
	// everybody at once. Retiring an instrument cancels reservations people
	// have planned their week around, and there is no undo that gives them
	// their slot back. Editing a resource's description is not here — it
	// inconveniences nobody, and a matrix that covers everything is one
	// somebody turns off.
	ActionResourcesRetire Action = "resources.retire"
)

// actionPattern is the same shape a permission has: dotted, lowercase, no
// wildcards. An action is a name in a log and an audit record as well as a map
// key, and a free-form string would eventually be one with a space in it.
var actionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// Action names one thing a person can ask this application to do.
//
// It is deliberately not an authz.Permission. The two answer different
// questions — "may you" and "have you proved it is still you" — and the axes do
// not line up: changing your own password is sensitive and needs no permission,
// while reading the audit log needs one and no proof.
type Action string

// Valid reports whether a is a well-formed action name.
func (a Action) Valid() bool { return actionPattern.MatchString(string(a)) }

func (a Action) String() string { return string(a) }

// Entry is one row of the matrix.
type Entry struct {
	// Action is what the use case will check.
	Action Action
	// Window is how old a proof may be and still be accepted.
	Window time.Duration
}

// Matrix is the closed set of actions requiring a recent proof.
//
// The zero value is unusable; construct one with [NewMatrix] or
// [DefaultMatrix].
type Matrix struct {
	windows map[Action]session.Freshness
}

// NewMatrix validates one reauthentication matrix.
//
// A duplicate action is refused rather than resolved by order, because two
// lines declaring different windows for one action is a disagreement, and
// picking the last one silently picks a side.
func NewMatrix(entries ...Entry) (*Matrix, error) {
	windows := make(map[Action]session.Freshness, len(entries))
	for _, entry := range entries {
		if !entry.Action.Valid() {
			return nil, fmt.Errorf("reauth: %q is not a well-formed action name", entry.Action)
		}
		if _, duplicate := windows[entry.Action]; duplicate {
			return nil, fmt.Errorf("reauth: action %s is declared more than once", entry.Action)
		}
		freshness, err := session.NewFreshness(entry.Window)
		if err != nil {
			return nil, fmt.Errorf("reauth: action %s: %w", entry.Action, err)
		}
		windows[entry.Action] = freshness
	}
	return &Matrix{windows: windows}, nil
}

// DefaultMatrix builds the matrix this application ships.
func DefaultMatrix() (*Matrix, error) {
	return NewMatrix(
		Entry{Action: ActionRolesGrant, Window: WindowEscalation},
		Entry{Action: ActionUsersEnable, Window: WindowAdministrative},
		Entry{Action: ActionInvitationsCreate, Window: WindowAdministrative},
		Entry{Action: ActionSecondFactorDisable, Window: WindowEscalation},
		Entry{Action: ActionResourcesRetire, Window: WindowAdministrative},
	)
}

// Freshness returns the window declared for an action, and whether one was.
//
// An undeclared action reports the zero window, which satisfies nothing. That
// is the direction a missing declaration has to fail in: an application whose
// matrix lost a line should stop doing the thing loudly, not keep doing it
// without the proof.
func (m *Matrix) Freshness(action Action) (session.Freshness, bool) {
	if m == nil {
		return session.Freshness{}, false
	}
	freshness, declared := m.windows[action]
	return freshness, declared
}

// Actions returns every declared action in a stable order, for a startup log
// line and for the test that proves the matrix matches the documentation.
func (m *Matrix) Actions() []Action {
	if m == nil {
		return nil
	}
	actions := make([]Action, 0, len(m.windows))
	for action := range m.windows {
		actions = append(actions, action)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i] < actions[j] })
	return actions
}
