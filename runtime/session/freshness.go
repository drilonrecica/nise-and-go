package session

import (
	"errors"
	"fmt"
	"time"
)

// Bounds on a reauthentication window.
//
// The minimum exists because a window shorter than the time it takes to be
// asked for a password, type it, and be returned to the action would make the
// action impossible rather than careful. The maximum exists because a window
// longer than a day is not reauthentication: it is a second session lifetime
// with a different name, and it would pass on the strength of a proof given
// before the laptop was left on a train.
const (
	// MinFreshness is the shortest reauthentication window.
	MinFreshness = 30 * time.Second
	// MaxFreshness is the longest reauthentication window.
	MaxFreshness = 24 * time.Hour
	// DefaultFreshness is the window this package suggests when an
	// application has no reason to choose another.
	DefaultFreshness = 15 * time.Minute
)

// ErrFreshness reports a reauthentication window outside the permitted bounds.
var ErrFreshness = errors.New("reauthentication window is outside the permitted bounds")

// Freshness is how recently the holder of a session must have proved
// themselves before one particular action.
//
// It is a separate question from whether the session is active. An active
// session says a credential was presented; a fresh proof says the person is
// still at the keyboard. The gap between the two is the unattended laptop, the
// borrowed phone, and the tab left open on a shared machine — none of which
// [Record.Evaluate] can see.
//
// The zero value is unusable, and deliberately unusable rather than
// permissive: an action whose window was never constructed must refuse, not
// accept every proof ever given.
type Freshness struct {
	window time.Duration
}

// NewFreshness validates one reauthentication window.
func NewFreshness(window time.Duration) (Freshness, error) {
	if window < MinFreshness || window > MaxFreshness {
		return Freshness{}, fmt.Errorf("%w: %s is outside %s..%s", ErrFreshness, window, MinFreshness, MaxFreshness)
	}
	return Freshness{window: window}, nil
}

// MustFreshness is NewFreshness for a package-level declaration, where an
// invalid window is a programming error that should stop the build's tests
// rather than reach a deployment.
func MustFreshness(window time.Duration) Freshness {
	freshness, err := NewFreshness(window)
	if err != nil {
		panic(err)
	}
	return freshness
}

// Window returns the permitted age of a proof.
func (f Freshness) Window() time.Duration { return f.window }

// IsZero reports whether f is the unconstructed zero value.
func (f Freshness) IsZero() bool { return f.window == 0 }

// Satisfied reports whether a proof given at provenAt is still good at now.
//
// A zero window refuses, a proof that was never given refuses, and a proof
// exactly as old as the window refuses: at the boundary the safe direction is
// to ask again.
//
// A proof timestamped after now is treated as current rather than as an error.
// That timestamp came from the server's own store, not from the caller, so a
// reader whose clock is a second behind the writer's is a clock problem and not
// an attack — and refusing there would log people out of an action they had
// just proved themselves for.
func (f Freshness) Satisfied(provenAt, now time.Time) bool {
	if f.IsZero() || provenAt.IsZero() {
		return false
	}
	age := now.Sub(provenAt)
	if age < 0 {
		age = 0
	}
	return age < f.window
}

// StaleAt is when a proof given at provenAt stops satisfying f.
//
// It exists so a caller can tell a person how long they have, rather than
// asking them to reauthenticate again a minute later.
func (f Freshness) StaleAt(provenAt time.Time) time.Time {
	if f.IsZero() || provenAt.IsZero() {
		return time.Time{}
	}
	return provenAt.Add(f.window).UTC()
}

// ProvenWithin reports whether this session carries a proof that satisfies f.
//
// It is deliberately not folded into [Record.Evaluate]. Evaluate answers a
// question with one answer for the whole request — may this session be used at
// all — while freshness has a different answer for each action, and a session
// that fails this check is still a valid session doing something ordinary.
func (r Record) ProvenWithin(now time.Time, freshness Freshness) bool {
	return freshness.Satisfied(r.ProvenAt, now)
}
