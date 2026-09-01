package pagination_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/pagination"
)

// fixedNow is the clock every deterministic test in this package reads.
var fixedNow = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

func testKey(t *testing.T, id string) pagination.Key {
	t.Helper()
	secret := make([]byte, pagination.MinKeyBytes)
	for i := range secret {
		secret[i] = byte(i) ^ id[0]
	}
	key, err := pagination.NewKey(id, secret)
	if err != nil {
		t.Fatalf("NewKey(%q): %v", id, err)
	}
	return key
}

func testCodec(t *testing.T, ttl time.Duration, ids ...string) *pagination.Codec {
	t.Helper()
	if len(ids) == 0 {
		ids = []string{"active"}
	}
	keys := make([]pagination.Key, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, testKey(t, id))
	}
	ring, err := pagination.NewKeyRing(keys[0], keys[1:]...)
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	codec, err := pagination.NewCodec(ring, ttl, pagination.WithClock(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	return codec
}

func testBinding() pagination.Binding {
	return pagination.NewBinding("/invoices", url.Values{"status": {"open"}})
}

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	binding := testBinding()
	values := []string{"2026-08-31T11:59:00Z", "01J0000000000000000000000"}

	token, err := codec.Encode(binding, pagination.Forward, values)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.HasPrefix(token, pagination.TokenVersion+".active.") {
		t.Errorf("token = %q, want version and active key prefix", token)
	}
	for _, value := range values {
		if strings.Contains(token, value) {
			t.Errorf("token %q leaks position value %q in the clear", token, value)
		}
	}

	cursor, err := codec.Decode(binding, token)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if cursor.Direction != pagination.Forward {
		t.Errorf("Direction = %q, want forward", cursor.Direction)
	}
	if len(cursor.Values) != len(values) {
		t.Fatalf("Values = %#v, want %#v", cursor.Values, values)
	}
	for i := range values {
		if cursor.Values[i] != values[i] {
			t.Errorf("Values[%d] = %q, want %q", i, cursor.Values[i], values[i])
		}
	}
	if want := fixedNow.Add(time.Hour); !cursor.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %s, want %s", cursor.ExpiresAt, want)
	}

	// A decoded cursor must not alias the codec's own state: mutating the
	// returned slice cannot change what a later decode of the same token
	// reports.
	cursor.Values[0] = "tampered"
	again, err := codec.Decode(binding, token)
	if err != nil {
		t.Fatalf("second Decode: %v", err)
	}
	if again.Values[0] != values[0] {
		t.Errorf("second decode saw %q, want %q", again.Values[0], values[0])
	}
}

func TestCursorEncodeIsDeterministic(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	binding := testBinding()
	first, err := codec.Encode(binding, pagination.Backward, []string{"42"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	second, err := codec.Encode(binding, pagination.Backward, []string{"42"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if first != second {
		t.Errorf("Encode is not deterministic at one instant:\n%q\n%q", first, second)
	}
}

func TestCursorRejectsTamperedTokens(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	binding := testBinding()
	token, err := codec.Encode(binding, pagination.Forward, []string{"41812"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	parts := strings.Split(token, ".")

	forged, err := json.Marshal(map[string]any{
		"d": "forward",
		"b": binding.String(),
		"x": fixedNow.Add(time.Hour).Unix(),
		"v": []string{"1"},
	})
	if err != nil {
		t.Fatalf("marshal forged payload: %v", err)
	}

	tests := []struct {
		name  string
		token string
		want  error
	}{
		{
			name:  "rewritten payload keeps a valid-looking tag",
			token: strings.Join([]string{parts[0], parts[1], base64.RawURLEncoding.EncodeToString(forged), parts[3]}, "."),
			want:  pagination.ErrCursorSignature,
		},
		{
			name:  "flipped tag byte",
			token: strings.Join([]string{parts[0], parts[1], parts[2], flipLast(parts[3])}, "."),
			want:  pagination.ErrCursorSignature,
		},
		{
			name:  "unknown key identifier",
			token: strings.Join([]string{parts[0], "other", parts[2], parts[3]}, "."),
			want:  pagination.ErrCursorSignature,
		},
		{
			name:  "unsupported version",
			token: strings.Join([]string{"nc2", parts[1], parts[2], parts[3]}, "."),
			want:  pagination.ErrCursorVersion,
		},
		{
			name:  "too few parts",
			token: strings.Join(parts[:3], "."),
			want:  pagination.ErrCursorMalformed,
		},
		{
			name:  "too many parts",
			token: token + ".extra",
			want:  pagination.ErrCursorMalformed,
		},
		{
			name:  "payload is not base64url",
			token: strings.Join([]string{parts[0], parts[1], "not base64!", parts[3]}, "."),
			want:  pagination.ErrCursorMalformed,
		},
		{
			name:  "tag is not base64url",
			token: strings.Join([]string{parts[0], parts[1], parts[2], "not base64!"}, "."),
			want:  pagination.ErrCursorMalformed,
		},
		{
			name:  "key identifier is not a permitted identifier",
			token: strings.Join([]string{parts[0], "UPPER/CASE", parts[2], parts[3]}, "."),
			want:  pagination.ErrCursorMalformed,
		},
		{
			name:  "empty token",
			token: "",
			want:  pagination.ErrCursorMalformed,
		},
		{
			name:  "oversized token",
			token: token + strings.Repeat("a", pagination.MaxTokenBytes),
			want:  pagination.ErrCursorMalformed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := codec.Decode(binding, tc.token); !errors.Is(err, tc.want) {
				t.Fatalf("Decode error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCursorRejectsHostilePayloads(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	binding := testBinding()

	oversize := make([]string, 0, pagination.MaxCursorValues+1)
	for i := 0; i <= pagination.MaxCursorValues; i++ {
		oversize = append(oversize, fmt.Sprint(i))
	}

	tests := []struct {
		name string
		body any
		want error
	}{
		{
			name: "unknown payload field",
			body: map[string]any{"d": "forward", "b": binding.String(), "x": fixedNow.Add(time.Hour).Unix(), "v": []string{"1"}, "extra": 1},
			want: pagination.ErrCursorMalformed,
		},
		{
			name: "direction outside the closed set",
			body: map[string]any{"d": "sideways", "b": binding.String(), "x": fixedNow.Add(time.Hour).Unix(), "v": []string{"1"}},
			want: pagination.ErrCursorMalformed,
		},
		{
			name: "no values",
			body: map[string]any{"d": "forward", "b": binding.String(), "x": fixedNow.Add(time.Hour).Unix(), "v": []string{}},
			want: pagination.ErrCursorMalformed,
		},
		{
			name: "too many values",
			body: map[string]any{"d": "forward", "b": binding.String(), "x": fixedNow.Add(time.Hour).Unix(), "v": oversize},
			want: pagination.ErrCursorMalformed,
		},
		{
			name: "oversized value",
			body: map[string]any{"d": "forward", "b": binding.String(), "x": fixedNow.Add(time.Hour).Unix(), "v": []string{strings.Repeat("x", pagination.MaxCursorValueBytes+1)}},
			want: pagination.ErrCursorMalformed,
		},
		{
			name: "payload is not an object",
			body: []string{"forward"},
			want: pagination.ErrCursorMalformed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			token := signedToken(t, tc.body)
			if _, err := codec.Decode(binding, token); !errors.Is(err, tc.want) {
				t.Fatalf("Decode error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCursorExpires(t *testing.T) {
	t.Parallel()

	issuer := testCodec(t, time.Minute)
	binding := testBinding()
	token, err := issuer.Encode(binding, pagination.Forward, []string{"7"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	ring, err := pagination.NewKeyRing(testKey(t, "active"))
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	at := func(offset time.Duration) *pagination.Codec {
		codec, err := pagination.NewCodec(ring, time.Minute, pagination.WithClock(func() time.Time {
			return fixedNow.Add(offset)
		}))
		if err != nil {
			t.Fatalf("NewCodec: %v", err)
		}
		return codec
	}

	if _, err := at(59*time.Second).Decode(binding, token); err != nil {
		t.Errorf("cursor rejected one second before expiry: %v", err)
	}
	if _, err := at(time.Minute).Decode(binding, token); !errors.Is(err, pagination.ErrCursorExpired) {
		t.Errorf("at expiry: error = %v, want ErrCursorExpired", err)
	}
	if _, err := at(2*time.Minute).Decode(binding, token); !errors.Is(err, pagination.ErrCursorExpired) {
		t.Errorf("after expiry: error = %v, want ErrCursorExpired", err)
	}
}

func TestCursorIsBoundToItsQuery(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	issued := pagination.NewBinding("/invoices", url.Values{"status": {"open"}})
	token, err := codec.Encode(issued, pagination.Forward, []string{"7"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	others := map[string]pagination.Binding{
		"different filter value":   pagination.NewBinding("/invoices", url.Values{"status": {"paid"}}),
		"additional filter":        pagination.NewBinding("/invoices", url.Values{"status": {"open"}, "customer": {"9"}}),
		"filter removed":           pagination.NewBinding("/invoices", nil),
		"different resource":       pagination.NewBinding("/payments", url.Values{"status": {"open"}}),
		"repeated value order":     pagination.NewBinding("/invoices", url.Values{"status": {"open", "paid"}}),
		"empty extra filter value": pagination.NewBinding("/invoices", url.Values{"status": {"open"}, "q": {""}}),
	}
	for name, other := range others {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := codec.Decode(other, token); !errors.Is(err, pagination.ErrCursorBinding) {
				t.Fatalf("Decode error = %v, want ErrCursorBinding", err)
			}
		})
	}

	t.Run("parameter order does not change the binding", func(t *testing.T) {
		t.Parallel()
		reordered := pagination.NewBinding("/invoices", url.Values{"status": {"open"}})
		if reordered.String() != issued.String() {
			t.Fatalf("binding = %q, want %q", reordered, issued)
		}
	})

	t.Run("repeated value order changes the binding", func(t *testing.T) {
		t.Parallel()
		first := pagination.NewBinding("/invoices", url.Values{"tag": {"a", "b"}})
		second := pagination.NewBinding("/invoices", url.Values{"tag": {"b", "a"}})
		if first.String() == second.String() {
			t.Fatal("repeated-parameter order collapsed into one binding")
		}
	})

	t.Run("adjacent fields cannot be shifted", func(t *testing.T) {
		t.Parallel()
		first := pagination.NewBinding("/invoices", url.Values{"ab": {"c"}})
		second := pagination.NewBinding("/invoices", url.Values{"a": {"bc"}})
		if first.String() == second.String() {
			t.Fatal("length-prefixing did not separate adjacent binding fields")
		}
	})
}

func TestCursorRequiresABinding(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	if _, err := codec.Encode(pagination.Binding{}, pagination.Forward, []string{"1"}); !errors.Is(err, pagination.ErrBinding) {
		t.Errorf("Encode with zero binding: error = %v, want ErrBinding", err)
	}
	if _, err := codec.Decode(pagination.Binding{}, "anything"); !errors.Is(err, pagination.ErrBinding) {
		t.Errorf("Decode with zero binding: error = %v, want ErrBinding", err)
	}
	if !(pagination.Binding{}).IsZero() {
		t.Error("zero Binding does not report IsZero")
	}
	if testBinding().IsZero() {
		t.Error("constructed Binding reports IsZero")
	}
}

func TestCursorEncodeRefusesInvalidInput(t *testing.T) {
	t.Parallel()

	codec := testCodec(t, time.Hour)
	binding := testBinding()

	tests := []struct {
		name      string
		direction pagination.Direction
		values    []string
		want      error
	}{
		{name: "direction outside the closed set", direction: "sideways", values: []string{"1"}, want: pagination.ErrDirection},
		{name: "empty direction", direction: "", values: []string{"1"}, want: pagination.ErrDirection},
		{name: "no values", direction: pagination.Forward, values: nil, want: pagination.ErrCursorValues},
		{name: "too many values", direction: pagination.Forward, values: make([]string, pagination.MaxCursorValues+1), want: pagination.ErrCursorValues},
		{name: "oversized value", direction: pagination.Forward, values: []string{strings.Repeat("x", pagination.MaxCursorValueBytes+1)}, want: pagination.ErrCursorValues},
		{name: "value is not UTF-8", direction: pagination.Forward, values: []string{"\xff\xfe"}, want: pagination.ErrCursorValues},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := codec.Encode(binding, tc.direction, tc.values); !errors.Is(err, tc.want) {
				t.Fatalf("Encode error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestKeyRotationKeepsOutstandingCursorsValid(t *testing.T) {
	t.Parallel()

	binding := testBinding()
	old := testCodec(t, time.Hour, "y2026a")
	token, err := old.Encode(binding, pagination.Forward, []string{"7"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	rotated := testCodec(t, time.Hour, "y2026b", "y2026a")
	if got := rotated.KeyRing().ActiveID(); got != "y2026b" {
		t.Fatalf("ActiveID = %q, want y2026b", got)
	}
	if _, err := rotated.Decode(binding, token); err != nil {
		t.Fatalf("cursor issued under the retired key was rejected after rotation: %v", err)
	}

	fresh, err := rotated.Encode(binding, pagination.Forward, []string{"8"})
	if err != nil {
		t.Fatalf("Encode after rotation: %v", err)
	}
	if !strings.HasPrefix(fresh, pagination.TokenVersion+".y2026b.") {
		t.Errorf("new cursor = %q, want signing by the active key", fresh)
	}

	retiredDropped := testCodec(t, time.Hour, "y2026b")
	if _, err := retiredDropped.Decode(binding, token); !errors.Is(err, pagination.ErrCursorSignature) {
		t.Errorf("after dropping the retired key: error = %v, want ErrCursorSignature", err)
	}
	if _, err := retiredDropped.Decode(binding, fresh); err != nil {
		t.Errorf("active-key cursor rejected after dropping the retired key: %v", err)
	}
}

func TestNewCodecValidatesItsInputs(t *testing.T) {
	t.Parallel()

	ring, err := pagination.NewKeyRing(testKey(t, "active"))
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	if _, err := pagination.NewCodec(nil, time.Hour); err == nil {
		t.Error("NewCodec accepted a nil key ring")
	}
	for _, ttl := range []time.Duration{0, -time.Second} {
		if _, err := pagination.NewCodec(ring, ttl); !errors.Is(err, pagination.ErrTTL) {
			t.Errorf("NewCodec(ttl=%s): error = %v, want ErrTTL", ttl, err)
		}
	}
	codec, err := pagination.NewCodec(ring, 90*time.Second, pagination.WithClock(nil))
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	if codec.TTL() != 90*time.Second {
		t.Errorf("TTL = %s, want 1m30s", codec.TTL())
	}
	if codec.KeyRing() != ring {
		t.Error("KeyRing did not return the configured ring")
	}
}

func TestParseDirection(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"forward", "backward"} {
		direction, err := pagination.ParseDirection(raw)
		if err != nil {
			t.Fatalf("ParseDirection(%q): %v", raw, err)
		}
		if string(direction) != raw || !direction.Valid() {
			t.Errorf("ParseDirection(%q) = %q", raw, direction)
		}
	}
	for _, raw := range []string{"", "Forward", "next", "asc"} {
		if _, err := pagination.ParseDirection(raw); !errors.Is(err, pagination.ErrDirection) {
			t.Errorf("ParseDirection(%q): error = %v, want ErrDirection", raw, err)
		}
	}
}

// signedToken produces a token whose tag is genuinely correct for body, so a
// test can exercise payload validation rather than signature validation.
//
// The tag is recomputed here from the format this package documents rather
// than borrowed from an unexported helper: if the framing ever changes without
// the documentation changing with it, these tests stop verifying and say so.
func signedToken(t *testing.T, body any) string {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	secret := make([]byte, pagination.MinKeyBytes)
	for i := range secret {
		secret[i] = byte(i) ^ 'a'
	}
	mac := hmac.New(sha256.New, secret)
	for _, field := range [][]byte{[]byte(pagination.TokenVersion), []byte("active"), encoded} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		mac.Write(length[:])
		mac.Write(field)
	}
	return strings.Join([]string{
		pagination.TokenVersion,
		"active",
		base64.RawURLEncoding.EncodeToString(encoded),
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)),
	}, ".")
}

func flipLast(encoded string) string {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 {
		return encoded + "A"
	}
	raw[len(raw)-1] ^= 0x01
	return base64.RawURLEncoding.EncodeToString(raw)
}

// FuzzCursorDecode asserts the one property decoding must hold for arbitrary
// input: it either returns a bounded, well-formed cursor or one of this
// package's declared errors. It must never panic and never accept a token it
// did not sign.
func FuzzCursorDecode(f *testing.F) {
	secret := make([]byte, pagination.MinKeyBytes)
	for i := range secret {
		secret[i] = byte(i) ^ 'a'
	}
	key, err := pagination.NewKey("active", secret)
	if err != nil {
		f.Fatalf("NewKey: %v", err)
	}
	ring, err := pagination.NewKeyRing(key)
	if err != nil {
		f.Fatalf("NewKeyRing: %v", err)
	}
	codec, err := pagination.NewCodec(ring, time.Hour, pagination.WithClock(func() time.Time { return fixedNow }))
	if err != nil {
		f.Fatalf("NewCodec: %v", err)
	}
	binding := testBinding()
	valid, err := codec.Encode(binding, pagination.Forward, []string{"41812"})
	if err != nil {
		f.Fatalf("Encode: %v", err)
	}
	f.Add(valid)
	for _, seed := range []string{
		"",
		".",
		"nc1",
		"nc1.active..",
		"nc1.active.e30.e30",
		"nc1.active." + base64.RawURLEncoding.EncodeToString([]byte(`{"d":"forward","b":"","x":0,"v":[]}`)) + ".AAAA",
		"nc2.active.AAAA.AAAA",
		strings.Repeat("a", pagination.MaxTokenBytes+1),
		"nc1.active.\x00\x01.\xff",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, token string) {
		cursor, err := codec.Decode(binding, token)
		if err != nil {
			for _, declared := range []error{
				pagination.ErrCursorMalformed,
				pagination.ErrCursorVersion,
				pagination.ErrCursorSignature,
				pagination.ErrCursorExpired,
				pagination.ErrCursorBinding,
			} {
				if errors.Is(err, declared) {
					return
				}
			}
			t.Fatalf("Decode(%q) returned an undeclared error: %v", token, err)
		}
		if !cursor.Direction.Valid() {
			t.Fatalf("Decode(%q) accepted direction %q", token, cursor.Direction)
		}
		if len(cursor.Values) == 0 || len(cursor.Values) > pagination.MaxCursorValues {
			t.Fatalf("Decode(%q) accepted %d values", token, len(cursor.Values))
		}
		for _, value := range cursor.Values {
			if len(value) > pagination.MaxCursorValueBytes {
				t.Fatalf("Decode(%q) accepted a value of %d bytes", token, len(value))
			}
		}
	})
}

// FuzzBindingIsInjective is the filter-side fuzz target.
//
// A binding is what stops a cursor issued against one filter set from being
// replayed against another — the cursor carries a fingerprint of the query
// that produced it, and a mismatch is refused. That guarantee rests entirely
// on two different filter sets never producing the same fingerprint, and on
// one filter set always producing the same one.
//
// Both are properties of the canonical encoding, which is where a mistake
// would live: a separator that can appear inside a value, a length prefix
// that is written for one field and not another, or a sort that reorders
// something whose order is meaningful. Every one of those makes two distinct
// queries collide, and a collision is a cursor that walks rows the current
// filter was never allowed to see.
func FuzzBindingIsInjective(f *testing.F) {
	f.Add("widgets", "status", "open", "owner", "alice")
	f.Add("widgets", "status", "openowner", "alice", "")
	f.Add("widgets", "", "", "", "")
	f.Add("", "a", "b", "c", "d")
	f.Add("widgets", "a\x00b", "c", "d", "e")
	f.Add("widgets", "a", "b&c=d", "e", "f")
	f.Add("widgets\x1f", "a", "b", "c", "d")

	f.Fuzz(func(t *testing.T, resource, keyA, valueA, keyB, valueB string) {
		first := pagination.NewBinding(resource, url.Values{keyA: {valueA}, keyB: {valueB}})
		second := pagination.NewBinding(resource, url.Values{keyA: {valueA}, keyB: {valueB}})

		// Deterministic: the same filters must fingerprint the same way, or
		// a cursor stops verifying against the query that produced it.
		if first.String() != second.String() {
			t.Fatalf("the same filters produced two fingerprints: %s and %s", first, second)
		}

		// A fingerprint is hexadecimal and fixed-width, so it can be logged
		// and compared without escaping.
		if len(first.String()) != 64 {
			t.Fatalf("fingerprint %q is %d characters, want 64", first, len(first.String()))
		}
		if _, err := hex.DecodeString(first.String()); err != nil {
			t.Fatalf("fingerprint %q is not hexadecimal: %v", first, err)
		}

		// And it must depend on the resource. Two resources sharing a
		// fingerprint would let a cursor issued for one walk the other.
		if other := pagination.NewBinding(resource+"x", url.Values{keyA: {valueA}, keyB: {valueB}}); other.String() == first.String() {
			t.Fatalf("resources %q and %qx share a fingerprint", resource, resource)
		}

		// Adding a filter must change it. This is the case a naive
		// concatenation gets wrong: "status=open" and "statu=sopen" encode
		// to the same bytes without length prefixes.
		if keyA != "" {
			extra := pagination.NewBinding(resource, url.Values{keyA: {valueA}, keyB: {valueB}, keyA + "z": {"1"}})
			if extra.String() == first.String() {
				t.Fatalf("adding a filter did not change the fingerprint (%s)", first)
			}
		}
	})
}

// FuzzParsePageRejectsWithoutPanicking covers the whole query-parameter
// surface a list endpoint exposes, which is the one place every request
// reaches unauthenticated input.
//
// What is asserted is not that a particular value is refused — that is what
// the table tests are for — but that no value produces a panic, a limit
// outside the configured bounds, or a page that claims a cursor it did not
// verify.
func FuzzParsePageRejectsWithoutPanicking(f *testing.F) {
	f.Add("25", "", "asc")
	f.Add("0", "", "")
	f.Add("-1", "abc", "desc")
	f.Add("99999999999999999999", "", "")
	f.Add("1e3", "cur_", "sideways")
	f.Add("\x00", "\x00", "\x00")
	f.Add(" 25 ", "%%%", "ASC")

	codec := fuzzCodec(f)
	limits, err := pagination.NewLimits(25, 100)
	if err != nil {
		f.Fatalf("NewLimits: %v", err)
	}
	binding := pagination.NewBinding("widgets", url.Values{"status": {"open"}})

	f.Fuzz(func(t *testing.T, limit, cursor, direction string) {
		query := url.Values{}
		if limit != "" {
			query.Set("limit", limit)
		}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if direction != "" {
			query.Set("direction", direction)
		}

		page, err := codec.ParsePage(binding, query, limits)
		if err != nil {
			// A refusal is a fine outcome for arbitrary input. What must
			// never happen is a refusal that also returns a usable page.
			if page.Limit != 0 || page.HasCursor {
				t.Fatalf("ParsePage refused %q but returned a usable page (limit %d, cursor %t)",
					query.Encode(), page.Limit, page.HasCursor)
			}
			return
		}

		if page.Limit < 1 || page.Limit > limits.Max() {
			t.Fatalf("ParsePage accepted %q and returned limit %d, outside 1..%d",
				query.Encode(), page.Limit, limits.Max())
		}
	})
}

// fuzzCodec builds a codec for the fuzz targets above. It cannot reuse
// testCodec, which takes a *testing.T.
func fuzzCodec(f *testing.F) *pagination.Codec {
	f.Helper()

	key, err := pagination.NewKey("fuzz", bytes.Repeat([]byte{0x2a}, 32))
	if err != nil {
		f.Fatalf("NewKey: %v", err)
	}
	ring, err := pagination.NewKeyRing(key)
	if err != nil {
		f.Fatalf("NewKeyRing: %v", err)
	}
	codec, err := pagination.NewCodec(ring, time.Hour)
	if err != nil {
		f.Fatalf("NewCodec: %v", err)
	}
	return codec
}
