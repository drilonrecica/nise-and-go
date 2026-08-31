package session

import (
	"errors"
	"fmt"
	"time"
)

// Bounds on a session lifetime. The maximums exist so a mistyped configuration
// is a startup failure rather than a session that outlives the employment it
// was created during.
const (
	// MinIdle is the shortest idle window a session may have.
	MinIdle = time.Minute
	// MaxIdle is the longest idle window a session may have.
	MaxIdle = 30 * 24 * time.Hour
	// MinAbsolute is the shortest absolute lifetime a session may have.
	MinAbsolute = time.Minute
	// MaxAbsolute is the longest absolute lifetime a session may have.
	MaxAbsolute = 365 * 24 * time.Hour
)

// Defaults this package ships, from the product's stated session policy.
const (
	// DefaultIdle is the default idle window: half a working day, so a
	// forgotten browser on a shared machine stops being a way in before the
	// next shift.
	DefaultIdle = 12 * time.Hour
	// DefaultAbsolute is the default absolute lifetime.
	DefaultAbsolute = 30 * 24 * time.Hour
	// DefaultTouchInterval is how stale the recorded activity time may get
	// before a request writes it back.
	DefaultTouchInterval = 5 * time.Minute
)

// ErrLifetime reports a lifetime outside the permitted bounds.
var ErrLifetime = errors.New("session lifetime is outside the permitted bounds")

// State is the closed set of conclusions [Record.Evaluate] can reach.
type State string

const (
	// Active means the session may be used.
	Active State = "active"
	// Revoked means someone ended the session deliberately.
	Revoked State = "revoked"
	// Expired means the absolute deadline has passed.
	Expired State = "expired"
	// Idle means the session was unused for longer than the idle window.
	Idle State = "idle"
)

// Valid reports whether s is one of the defined states.
func (s State) Valid() bool {
	switch s {
	case Active, Revoked, Expired, Idle:
		return true
	default:
		return false
	}
}

// Lifetime is one deployment's session policy.
//
// The zero value is unusable; construct one with [NewLifetime].
type Lifetime struct {
	idle          time.Duration
	absolute      time.Duration
	touchInterval time.Duration
}

// NewLifetime validates one session policy.
//
// touchInterval is how stale the recorded activity time may get before a
// request writes it back. It has to be well inside the idle window — a session
// whose activity is recorded less often than it expires would be logged out
// while in use — and it is what keeps an authenticated GET from being a
// database write.
func NewLifetime(idle, absolute, touchInterval time.Duration) (Lifetime, error) {
	switch {
	case idle < MinIdle || idle > MaxIdle:
		return Lifetime{}, fmt.Errorf("%w: idle %s is outside %s..%s", ErrLifetime, idle, MinIdle, MaxIdle)
	case absolute < MinAbsolute || absolute > MaxAbsolute:
		return Lifetime{}, fmt.Errorf("%w: absolute %s is outside %s..%s", ErrLifetime, absolute, MinAbsolute, MaxAbsolute)
	case idle > absolute:
		return Lifetime{}, fmt.Errorf("%w: idle %s exceeds absolute %s", ErrLifetime, idle, absolute)
	case touchInterval <= 0:
		return Lifetime{}, fmt.Errorf("%w: touch interval %s must be greater than zero", ErrLifetime, touchInterval)
	case touchInterval*2 > idle:
		return Lifetime{}, fmt.Errorf("%w: touch interval %s is more than half the idle window %s", ErrLifetime, touchInterval, idle)
	}
	return Lifetime{idle: idle, absolute: absolute, touchInterval: touchInterval}, nil
}

// DefaultLifetime returns the policy this package ships.
func DefaultLifetime() Lifetime {
	return Lifetime{idle: DefaultIdle, absolute: DefaultAbsolute, touchInterval: DefaultTouchInterval}
}

// Idle returns the idle window.
func (l Lifetime) Idle() time.Duration { return l.idle }

// Absolute returns the absolute lifetime.
func (l Lifetime) Absolute() time.Duration { return l.absolute }

// TouchInterval returns how stale recorded activity may get.
func (l Lifetime) TouchInterval() time.Duration { return l.touchInterval }

// IsZero reports whether l is the unconstructed zero value.
func (l Lifetime) IsZero() bool { return l.idle == 0 }

// ExpiresAt is the absolute deadline for a session created at issuedAt.
//
// The result is stored on the row. Recomputing it from configuration on every
// request would mean a change to the policy silently extended, or silently
// ended, sessions that already existed.
func (l Lifetime) ExpiresAt(issuedAt time.Time) time.Time {
	return issuedAt.Add(l.absolute).UTC()
}

// NeedsTouch reports whether the recorded activity time is stale enough to be
// worth a write.
func (l Lifetime) NeedsTouch(lastSeenAt, now time.Time) bool {
	return now.Sub(lastSeenAt) >= l.touchInterval
}

// Record is the driver-independent view of a stored session.
//
// It carries no token and no digest. Whatever loaded it has already matched
// the credential; passing the credential further would only widen where it can
// be logged.
type Record struct {
	// ID identifies the session for revocation and for the account's own
	// list of active sessions.
	ID string
	// UserID is the subject the session authenticates.
	UserID string
	// CreatedAt is when the session was issued.
	CreatedAt time.Time
	// LastSeenAt is the recorded activity time, which slides no more often
	// than the touch interval.
	LastSeenAt time.Time
	// ExpiresAt is the absolute deadline written when the session was
	// created.
	ExpiresAt time.Time
	// RevokedAt is when the session was revoked, or the zero time.
	RevokedAt time.Time
}

// Evaluate reports whether the session may be used, and if not, the strongest
// reason why.
//
// The order is deliberate. A revoked session that has also expired is revoked:
// that is what an operator asked for and what an audit record should say. An
// expired session that is also idle is expired, because the absolute deadline
// is the bound that cannot be slid.
func (r Record) Evaluate(now time.Time, lifetime Lifetime) State {
	switch {
	case !r.RevokedAt.IsZero() && !now.Before(r.RevokedAt):
		return Revoked
	case !r.ExpiresAt.IsZero() && !now.Before(r.ExpiresAt):
		return Expired
	case lifetime.IsZero():
		// A record cannot be judged against a policy that was never
		// constructed. Failing closed here keeps a wiring mistake from
		// silently accepting every session forever.
		return Idle
	case now.Sub(r.LastSeenAt) >= lifetime.idle:
		return Idle
	default:
		return Active
	}
}

// IsActive reports whether the session may be used.
func (r Record) IsActive(now time.Time, lifetime Lifetime) bool {
	return r.Evaluate(now, lifetime) == Active
}
