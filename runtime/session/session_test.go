package session_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/session"
)

func TestNewTokenShape(t *testing.T) {
	t.Parallel()

	token, err := session.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	value := token.Value()
	if !strings.HasPrefix(value, session.TokenVersion+"_") {
		t.Fatalf("token = %q, want the version prefix", value)
	}
	if token.IsZero() {
		t.Error("a minted token reports IsZero")
	}
	if !(session.Token{}).IsZero() {
		t.Error("the zero token does not report IsZero")
	}

	secret := strings.TrimPrefix(value, session.TokenVersion+"_")
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	if err != nil {
		t.Fatalf("the token secret is not strict base64url: %v", err)
	}
	if len(decoded) != session.SecretBytes {
		t.Fatalf("token carries %d bytes of randomness, want %d", len(decoded), session.SecretBytes)
	}
	if len(token.Digest()) != session.DigestBytes {
		t.Fatalf("digest is %d bytes, want %d", len(token.Digest()), session.DigestBytes)
	}
}

func TestNewTokensAreUnique(t *testing.T) {
	t.Parallel()

	const count = 512
	seen := make(map[string]struct{}, count)
	for range count {
		token, err := session.New()
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, repeat := seen[token.Value()]; repeat {
			t.Fatal("New produced the same token twice")
		}
		seen[token.Value()] = struct{}{}
	}
}

func TestTokenIsUnprintable(t *testing.T) {
	t.Parallel()

	token, err := session.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	secret := token.Value()

	rendered := []string{
		fmt.Sprintf("%v %s %q %#v", token, token, token, token),
		fmt.Sprint(slog.AnyValue(token).Resolve()),
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	rendered = append(rendered, string(encoded))
	text, err := token.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	rendered = append(rendered, string(text))

	for _, output := range rendered {
		if strings.Contains(output, secret) {
			t.Errorf("output exposed the token: %q", output)
		}
		if !strings.Contains(output, "REDACTED") {
			t.Errorf("output is not the redaction placeholder: %q", output)
		}
	}

	// A token inside a struct must be redacted too: that is how one reaches
	// a log line in practice, not by being passed alone.
	type envelope struct {
		Token session.Token `json:"token"`
	}
	wrapped, err := json.Marshal(envelope{Token: token})
	if err != nil {
		t.Fatalf("Marshal envelope: %v", err)
	}
	if strings.Contains(string(wrapped), secret) {
		t.Errorf("a wrapped token exposed its value: %s", wrapped)
	}
}

func TestParseToken(t *testing.T) {
	t.Parallel()

	minted, err := session.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	parsed, err := session.Parse(minted.Value())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Value() != minted.Value() {
		t.Fatalf("Parse changed the token")
	}
	if !session.EqualDigest(parsed.Digest(), minted.Digest()) {
		t.Fatal("a parsed token has a different digest from the one that minted it")
	}

	valid := minted.Value()
	rejected := map[string]string{
		"empty":                    "",
		"no prefix":                strings.TrimPrefix(valid, session.TokenVersion+"_"),
		"wrong prefix":             "ns2" + strings.TrimPrefix(valid, session.TokenVersion),
		"wrong separator":          strings.Replace(valid, "_", ".", 1),
		"truncated secret":         valid[:len(valid)-1],
		"lengthened secret":        valid + "A",
		"padded base64":            session.TokenVersion + "_" + base64.URLEncoding.EncodeToString(make([]byte, session.SecretBytes)),
		"standard base64 alphabet": session.TokenVersion + "_" + strings.Repeat("+", 43),
		"not base64":               session.TokenVersion + "_" + strings.Repeat("!", 43),
		"oversized":                session.TokenVersion + "_" + strings.Repeat("A", session.MaxTokenBytes),
		"path traversal":           "../../etc/passwd",
		"embedded newline":         session.TokenVersion + "_\n" + strings.Repeat("A", 42),
	}
	for name, raw := range rejected {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := session.Parse(raw); !errors.Is(err, session.ErrTokenFormat) {
				t.Fatalf("Parse(%q) error = %v, want ErrTokenFormat", raw, err)
			}
			if _, err := session.Digest(raw); !errors.Is(err, session.ErrTokenFormat) {
				t.Fatalf("Digest(%q) error = %v, want ErrTokenFormat", raw, err)
			}
		})
	}
}

func TestDigestIsStableAndTokenBound(t *testing.T) {
	t.Parallel()

	first, err := session.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	second, err := session.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	lookup, err := session.Digest(first.Value())
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if !session.EqualDigest(lookup, first.Digest()) {
		t.Fatal("Digest(value) does not match Token.Digest")
	}
	if session.EqualDigest(first.Digest(), second.Digest()) {
		t.Fatal("two tokens share a digest")
	}
	// The digest must not be reversible to the token by inspection: it is
	// binary, and the token is not a substring of it.
	if strings.Contains(string(first.Digest()), first.Value()) {
		t.Fatal("the digest contains the token")
	}

	clone := session.CloneDigest(first.Digest())
	clone[0] ^= 0xff
	if session.EqualDigest(clone, first.Digest()) {
		t.Fatal("EqualDigest accepted a modified digest")
	}
	if !session.EqualDigest(session.CloneDigest(first.Digest()), first.Digest()) {
		t.Fatal("CloneDigest did not preserve the digest")
	}
	for _, wrong := range [][]byte{nil, {}, make([]byte, session.DigestBytes-1), make([]byte, session.DigestBytes+1)} {
		if session.EqualDigest(wrong, first.Digest()) {
			t.Errorf("EqualDigest accepted a %d-byte value", len(wrong))
		}
	}
}

func TestNewLifetime(t *testing.T) {
	t.Parallel()

	lifetime, err := session.NewLifetime(12*time.Hour, 30*24*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewLifetime: %v", err)
	}
	if lifetime.Idle() != 12*time.Hour || lifetime.Absolute() != 30*24*time.Hour || lifetime.TouchInterval() != 5*time.Minute {
		t.Fatalf("lifetime = %#v", lifetime)
	}
	if lifetime.IsZero() {
		t.Error("a constructed lifetime reports IsZero")
	}
	if !(session.Lifetime{}).IsZero() {
		t.Error("the zero lifetime does not report IsZero")
	}

	tests := []struct {
		name     string
		idle     time.Duration
		absolute time.Duration
		touch    time.Duration
	}{
		{name: "idle below the floor", idle: session.MinIdle - time.Second, absolute: time.Hour, touch: time.Second},
		{name: "idle above the ceiling", idle: session.MaxIdle + time.Hour, absolute: session.MaxAbsolute, touch: time.Minute},
		{name: "absolute below the floor", idle: time.Minute, absolute: session.MinAbsolute - time.Second, touch: time.Second},
		{name: "absolute above the ceiling", idle: time.Hour, absolute: session.MaxAbsolute + time.Hour, touch: time.Minute},
		{name: "idle beyond absolute", idle: 2 * time.Hour, absolute: time.Hour, touch: time.Minute},
		{name: "no touch interval", idle: time.Hour, absolute: time.Hour, touch: 0},
		{name: "negative touch interval", idle: time.Hour, absolute: time.Hour, touch: -time.Minute},
		{name: "touch interval beyond half the idle window", idle: time.Hour, absolute: time.Hour, touch: 31 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := session.NewLifetime(tc.idle, tc.absolute, tc.touch); !errors.Is(err, session.ErrLifetime) {
				t.Fatalf("NewLifetime error = %v, want ErrLifetime", err)
			}
		})
	}
}

func TestDefaultLifetimeMatchesThePolicy(t *testing.T) {
	t.Parallel()

	shipped := session.DefaultLifetime()
	if shipped.Idle() != 12*time.Hour {
		t.Errorf("default idle = %s, want the stated 12h", shipped.Idle())
	}
	if shipped.Absolute() != 30*24*time.Hour {
		t.Errorf("default absolute = %s, want the stated 30d", shipped.Absolute())
	}
	if _, err := session.NewLifetime(shipped.Idle(), shipped.Absolute(), shipped.TouchInterval()); err != nil {
		t.Errorf("the shipped default does not satisfy NewLifetime: %v", err)
	}
}

func TestExpiresAtIsFixedAtIssue(t *testing.T) {
	t.Parallel()

	issued := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	short, err := session.NewLifetime(time.Hour, 24*time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("NewLifetime: %v", err)
	}
	if got, want := short.ExpiresAt(issued), issued.Add(24*time.Hour); !got.Equal(want) {
		t.Fatalf("ExpiresAt = %s, want %s", got, want)
	}

	// The deadline stored on the row is what a later policy change must not
	// move. Evaluate reads the record, never the policy's absolute bound.
	record := session.Record{
		ID: "s1", UserID: "u1",
		CreatedAt:  issued,
		LastSeenAt: issued,
		ExpiresAt:  short.ExpiresAt(issued),
	}
	longer, err := session.NewLifetime(time.Hour, 365*24*time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("NewLifetime: %v", err)
	}
	if got := record.Evaluate(issued.Add(25*time.Hour), longer); got != session.Expired {
		t.Fatalf("state = %q under a lengthened policy, want expired", got)
	}
}

func TestEvaluateReportsTheStrongestReason(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	lifetime, err := session.NewLifetime(time.Hour, 24*time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("NewLifetime: %v", err)
	}
	active := session.Record{
		ID: "s1", UserID: "u1",
		CreatedAt:  now.Add(-time.Minute),
		LastSeenAt: now.Add(-time.Minute),
		ExpiresAt:  now.Add(23 * time.Hour),
	}

	tests := []struct {
		name   string
		mutate func(session.Record) session.Record
		want   session.State
	}{
		{name: "fresh", mutate: func(r session.Record) session.Record { return r }, want: session.Active},
		{
			name:   "one second inside the idle window",
			mutate: func(r session.Record) session.Record { r.LastSeenAt = now.Add(-time.Hour + time.Second); return r },
			want:   session.Active,
		},
		{
			name:   "exactly at the idle window",
			mutate: func(r session.Record) session.Record { r.LastSeenAt = now.Add(-time.Hour); return r },
			want:   session.Idle,
		},
		{
			name:   "exactly at the absolute deadline",
			mutate: func(r session.Record) session.Record { r.ExpiresAt = now; return r },
			want:   session.Expired,
		},
		{
			name: "expired outranks idle",
			mutate: func(r session.Record) session.Record {
				r.LastSeenAt = now.Add(-2 * time.Hour)
				r.ExpiresAt = now.Add(-time.Minute)
				return r
			},
			want: session.Expired,
		},
		{
			name: "revoked outranks expired",
			mutate: func(r session.Record) session.Record {
				r.RevokedAt = now.Add(-time.Minute)
				r.ExpiresAt = now.Add(-time.Minute)
				r.LastSeenAt = now.Add(-2 * time.Hour)
				return r
			},
			want: session.Revoked,
		},
		{
			name:   "revoked in the future is not yet revoked",
			mutate: func(r session.Record) session.Record { r.RevokedAt = now.Add(time.Minute); return r },
			want:   session.Active,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			record := tc.mutate(active)
			if got := record.Evaluate(now, lifetime); got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
			if got := record.IsActive(now, lifetime); got != (tc.want == session.Active) {
				t.Errorf("IsActive = %t for state %q", got, tc.want)
			}
			if !record.Evaluate(now, lifetime).Valid() {
				t.Errorf("Evaluate returned an undefined state")
			}
		})
	}

	// A policy that was never constructed must not accept sessions forever.
	if got := active.Evaluate(now, session.Lifetime{}); got == session.Active {
		t.Error("an unconstructed lifetime accepted a session")
	}
}

func TestNeedsTouchBoundsTheWriteRate(t *testing.T) {
	t.Parallel()

	lifetime, err := session.NewLifetime(time.Hour, 24*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewLifetime: %v", err)
	}
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	if lifetime.NeedsTouch(now, now) {
		t.Error("a session seen this instant needs a write")
	}
	if lifetime.NeedsTouch(now.Add(-4*time.Minute-59*time.Second), now) {
		t.Error("a session seen inside the touch interval needs a write")
	}
	if !lifetime.NeedsTouch(now.Add(-5*time.Minute), now) {
		t.Error("a session at the touch interval was not written back")
	}
	if !lifetime.NeedsTouch(now.Add(-time.Hour), now) {
		t.Error("a long-stale session was not written back")
	}
}

func TestStateValid(t *testing.T) {
	t.Parallel()

	for _, state := range []session.State{session.Active, session.Revoked, session.Expired, session.Idle} {
		if !state.Valid() {
			t.Errorf("%q is not reported valid", state)
		}
	}
	for _, state := range []session.State{"", "ACTIVE", "logged_out"} {
		if state.Valid() {
			t.Errorf("%q is reported valid", state)
		}
	}
}

// FuzzParseToken asserts that token parsing has exactly one accepting shape and
// never panics. An accepted value must round-trip to a digest of the right
// width; everything else must be ErrTokenFormat.
func FuzzParseToken(f *testing.F) {
	minted, err := session.New()
	if err != nil {
		f.Fatalf("New: %v", err)
	}
	for _, seed := range []string{
		minted.Value(),
		"",
		"ns1_",
		"ns1_" + strings.Repeat("A", 43),
		"ns1_" + strings.Repeat("A", 42),
		"ns2_" + strings.Repeat("A", 43),
		strings.Repeat("A", session.MaxTokenBytes+1),
		"ns1_\x00\x01\x02",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		token, err := session.Parse(raw)
		if err != nil {
			if !errors.Is(err, session.ErrTokenFormat) {
				t.Fatalf("Parse(%q) returned an undeclared error: %v", raw, err)
			}
			return
		}
		if token.Value() != raw {
			t.Fatalf("Parse(%q) changed the token to %q", raw, token.Value())
		}
		if len(token.Digest()) != session.DigestBytes {
			t.Fatalf("Parse(%q) produced a %d-byte digest", raw, len(token.Digest()))
		}
		if token.String() != "[REDACTED]" {
			t.Fatalf("Parse(%q) produced a printable token", raw)
		}
	})
}
