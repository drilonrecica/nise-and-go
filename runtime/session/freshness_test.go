package session_test

import (
	"errors"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/session"
)

func TestNewFreshness(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		window time.Duration
		wantOK bool
	}{
		{name: "the default", window: session.DefaultFreshness, wantOK: true},
		{name: "the minimum", window: session.MinFreshness, wantOK: true},
		{name: "the maximum", window: session.MaxFreshness, wantOK: true},
		{name: "zero", window: 0},
		{name: "negative", window: -time.Minute},
		{name: "under the minimum", window: session.MinFreshness - time.Nanosecond},
		{name: "over the maximum", window: session.MaxFreshness + time.Nanosecond},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			freshness, err := session.NewFreshness(testCase.window)
			if testCase.wantOK {
				if err != nil {
					t.Fatalf("NewFreshness(%s) = %v, want a window", testCase.window, err)
				}
				if freshness.Window() != testCase.window {
					t.Fatalf("Window() = %s, want %s", freshness.Window(), testCase.window)
				}
				if freshness.IsZero() {
					t.Fatal("a constructed window reports itself as the zero value")
				}
				return
			}
			if err == nil {
				t.Fatalf("NewFreshness(%s) accepted a window outside the bounds", testCase.window)
			}
			if !errors.Is(err, session.ErrFreshness) {
				t.Fatalf("error %v does not match ErrFreshness", err)
			}
		})
	}
}

func TestMustFreshnessPanicsOnAnImpossibleWindow(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("MustFreshness accepted a window outside the bounds")
		}
	}()
	_ = session.MustFreshness(time.Nanosecond)
}

// A window that was never constructed must refuse every proof. The alternative
// — a zero value that accepts anything — turns a missing matrix entry into an
// action nobody has to prove themselves for.
func TestTheZeroWindowRefuses(t *testing.T) {
	t.Parallel()

	var freshness session.Freshness
	if !freshness.IsZero() {
		t.Fatal("the zero value does not report itself as the zero value")
	}
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	if freshness.Satisfied(now, now) {
		t.Fatal("the zero window accepted a proof given this instant")
	}
	if !freshness.StaleAt(now).IsZero() {
		t.Fatal("the zero window reported a staleness deadline")
	}
}

func TestSatisfiedMeasuresTheAgeOfTheProof(t *testing.T) {
	t.Parallel()

	window := 15 * time.Minute
	freshness := session.MustFreshness(window)
	proof := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	for _, testCase := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "the instant it was given", now: proof, want: true},
		{name: "a second before it goes stale", now: proof.Add(window - time.Second), want: true},
		// The boundary refuses: at exactly the window the safe direction is
		// to ask again.
		{name: "exactly at the window", now: proof.Add(window)},
		{name: "long after", now: proof.Add(48 * time.Hour)},
		// The proof timestamp comes from the server's own store. A reader
		// whose clock trails the writer's is a clock problem, not an attack,
		// and refusing there would ask for a password just given.
		{name: "a proof stamped ahead of the reader's clock", now: proof.Add(-time.Second), want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := freshness.Satisfied(proof, testCase.now); got != testCase.want {
				t.Fatalf("Satisfied(proof, %s) = %t, want %t", testCase.now, got, testCase.want)
			}
		})
	}
}

// A session that has never carried a proof is not fresh, whatever its other
// timestamps say.
func TestAProofThatWasNeverGivenRefuses(t *testing.T) {
	t.Parallel()

	freshness := session.MustFreshness(session.DefaultFreshness)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	if freshness.Satisfied(time.Time{}, now) {
		t.Fatal("a zero proof time satisfied a window")
	}

	record := session.Record{
		ID:         "a4c7f2d0-0000-4000-8000-000000000001",
		UserID:     "a4c7f2d0-0000-4000-8000-000000000002",
		CreatedAt:  now.Add(-time.Minute),
		LastSeenAt: now,
		ExpiresAt:  now.Add(24 * time.Hour),
	}
	if record.ProvenWithin(now, freshness) {
		t.Fatal("a record with no recorded proof was treated as fresh")
	}
}

func TestStaleAtIsTheEndOfTheWindow(t *testing.T) {
	t.Parallel()

	freshness := session.MustFreshness(5 * time.Minute)
	proof := time.Date(2026, 8, 31, 12, 0, 0, 0, time.FixedZone("east", 2*60*60))

	stale := freshness.StaleAt(proof)
	if want := proof.Add(5 * time.Minute).UTC(); !stale.Equal(want) {
		t.Fatalf("StaleAt = %s, want %s", stale, want)
	}
	if stale.Location() != time.UTC {
		t.Fatalf("StaleAt returned %s, want UTC", stale.Location())
	}
	if freshness.Satisfied(proof, stale) {
		t.Fatal("the proof was still satisfied at the instant it went stale")
	}
}

// Freshness and usability are separate questions. A session can be active and
// stale, which is the ordinary case an hour into an afternoon's work, and the
// two answers must not be folded into one.
func TestFreshnessIsSeparateFromUsability(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	lifetime := session.DefaultLifetime()
	freshness := session.MustFreshness(5 * time.Minute)

	record := session.Record{
		ID:         "a4c7f2d0-0000-4000-8000-000000000001",
		UserID:     "a4c7f2d0-0000-4000-8000-000000000002",
		CreatedAt:  now.Add(-2 * time.Hour),
		LastSeenAt: now.Add(-time.Minute),
		ExpiresAt:  now.Add(24 * time.Hour),
		ProvenAt:   now.Add(-2 * time.Hour),
	}
	if !record.IsActive(now, lifetime) {
		t.Fatal("the session is not usable, so the test proves nothing about freshness")
	}
	if record.ProvenWithin(now, freshness) {
		t.Fatal("a two-hour-old proof satisfied a five-minute window")
	}

	record.ProvenAt = now.Add(-time.Minute)
	if !record.ProvenWithin(now, freshness) {
		t.Fatal("a one-minute-old proof did not satisfy a five-minute window")
	}

	// A revoked session is not made usable by a proof given a moment ago.
	record.RevokedAt = now.Add(-time.Second)
	if record.IsActive(now, lifetime) {
		t.Fatal("a revoked session was reported active")
	}
}
