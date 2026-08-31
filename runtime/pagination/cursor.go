package pagination

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
	"io"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// TokenVersion is the prefix every cursor this package issues begins with. It
// is a value, not a number, so a future format is a different prefix rather
// than an integer comparison every decoder has to get right.
const TokenVersion = "nc1"

// Bounds every issued and accepted cursor is held to. They are deliberately
// small: a cursor carries a row's sort key, not a query, a filter set, or an
// application object.
const (
	// MaxTokenBytes is the longest cursor string [Codec.Decode] will look
	// at. It is checked before any decoding so an oversized value costs a
	// length comparison rather than an allocation.
	MaxTokenBytes = 4096
	// MaxCursorValues is the largest number of sort-key components one
	// cursor may carry.
	MaxCursorValues = 8
	// MaxCursorValueBytes is the longest single sort-key component.
	MaxCursorValueBytes = 256
)

// Errors reported by cursor encoding and decoding. Every one of them is a
// client error; none of them is a reason to return anything but one bounded
// public failure.
var (
	// ErrCursorMalformed reports a token whose structure, encoding, or
	// payload bounds are wrong.
	ErrCursorMalformed = errors.New("cursor is malformed")
	// ErrCursorVersion reports a token issued in a format this build does
	// not parse.
	ErrCursorVersion = errors.New("cursor version is not supported")
	// ErrCursorSignature reports a token whose authentication tag does not
	// verify under any key in the ring, including one naming an unknown key.
	ErrCursorSignature = errors.New("cursor signature does not verify")
	// ErrCursorExpired reports a token that verified but is past its expiry.
	ErrCursorExpired = errors.New("cursor has expired")
	// ErrCursorBinding reports a valid cursor replayed against a different
	// query than the one it was issued for.
	ErrCursorBinding = errors.New("cursor does not belong to this query")
	// ErrCursorDirection reports a cursor used in the opposite direction
	// from the one it was issued for.
	ErrCursorDirection = errors.New("cursor was issued for the other direction")
	// ErrBinding reports an unbound or invalid query binding.
	ErrBinding = errors.New("cursor query binding is not constructed")
	// ErrDirection reports a direction value outside the closed set.
	ErrDirection = errors.New("cursor direction is not forward or backward")
	// ErrCursorValues reports sort-key values outside this package's bounds.
	ErrCursorValues = errors.New("cursor values exceed the permitted bounds")
	// ErrTTL reports a non-positive cursor lifetime.
	ErrTTL = errors.New("cursor lifetime must be greater than zero")
)

// Direction is the closed set of directions a page may be read in.
type Direction string

const (
	// Forward reads the page after the cursor's position, in the query's
	// declared sort order.
	Forward Direction = "forward"
	// Backward reads the page before the cursor's position.
	Backward Direction = "backward"
)

// Valid reports whether d is one of the two defined directions.
func (d Direction) Valid() bool { return d == Forward || d == Backward }

// ParseDirection converts a wire value into a [Direction], refusing anything
// outside the closed set rather than defaulting.
func ParseDirection(raw string) (Direction, error) {
	direction := Direction(raw)
	if !direction.Valid() {
		return "", fmt.Errorf("%w: %q", ErrDirection, raw)
	}
	return direction, nil
}

// Binding is the fingerprint of the query a cursor belongs to: its resource
// and every filter that changes which rows the query returns.
//
// The zero value is unbound and is refused by [Codec.Encode], so a cursor
// cannot accidentally be issued without one.
type Binding struct {
	fingerprint string
}

// NewBinding fingerprints one query shape. resource names the collection;
// filters holds every request value that changes the result set, and must not
// include the pagination parameters themselves.
//
// Keys are sorted so that parameter order does not change the fingerprint.
// Values are not sorted: repeated parameters keep their request order, which
// is part of what the query means.
func NewBinding(resource string, filters url.Values) Binding {
	var canonical bytes.Buffer
	writeLengthPrefixed(&canonical, []byte(resource))

	keys := make([]string, 0, len(filters))
	for key := range filters {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	writeUint64(&canonical, uint64(len(keys)))
	for _, key := range keys {
		writeLengthPrefixed(&canonical, []byte(key))
		values := filters[key]
		writeUint64(&canonical, uint64(len(values)))
		for _, value := range values {
			writeLengthPrefixed(&canonical, []byte(value))
		}
	}

	sum := sha256.Sum256(canonical.Bytes())
	return Binding{fingerprint: hex.EncodeToString(sum[:])}
}

// IsZero reports whether b is the unbound zero value.
func (b Binding) IsZero() bool { return b.fingerprint == "" }

// String returns the binding's hexadecimal fingerprint. It is derived from
// request parameters, not from secret material, and is safe to log.
func (b Binding) String() string { return b.fingerprint }

// Cursor is one decoded position.
type Cursor struct {
	// Direction is the direction the cursor was issued for.
	Direction Direction
	// Values are the sort-key components identifying the row the page ended
	// on, in the query's sort order.
	Values []string
	// ExpiresAt is when the cursor stops being accepted.
	ExpiresAt time.Time
}

// Codec issues and verifies cursors under one key ring and lifetime.
//
// The zero value is unusable; construct one with [NewCodec].
type Codec struct {
	ring *KeyRing
	ttl  time.Duration
	now  func() time.Time
}

// Option configures a [Codec].
type Option func(*Codec)

// WithClock replaces the clock a codec reads for issuing and expiry. It exists
// for tests; production code leaves the default [time.Now] in place.
func WithClock(now func() time.Time) Option {
	return func(c *Codec) {
		if now != nil {
			c.now = now
		}
	}
}

// NewCodec returns a codec that signs with ring's active key and issues
// cursors valid for ttl.
func NewCodec(ring *KeyRing, ttl time.Duration, opts ...Option) (*Codec, error) {
	if ring == nil {
		return nil, errors.New("cursor codec requires a key ring")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("%w: %s", ErrTTL, ttl)
	}
	codec := &Codec{ring: ring, ttl: ttl, now: time.Now}
	for _, opt := range opts {
		opt(codec)
	}
	return codec, nil
}

// KeyRing returns the ring this codec verifies against.
func (c *Codec) KeyRing() *KeyRing { return c.ring }

// TTL returns the lifetime of cursors this codec issues.
func (c *Codec) TTL() time.Duration { return c.ttl }

// payload is the authenticated body of a cursor. Its field names are short
// because they are on the wire in every issued token; its shape is closed
// because decoding refuses unknown fields.
type payload struct {
	Direction string   `json:"d"`
	Binding   string   `json:"b"`
	ExpiresAt int64    `json:"x"`
	Values    []string `json:"v"`
}

// Encode issues one cursor for the given query, direction, and sort key.
func (c *Codec) Encode(binding Binding, direction Direction, values []string) (string, error) {
	if binding.IsZero() {
		return "", ErrBinding
	}
	if !direction.Valid() {
		return "", fmt.Errorf("%w: %q", ErrDirection, direction)
	}
	if err := checkValues(values); err != nil {
		return "", err
	}

	body, err := json.Marshal(payload{
		Direction: string(direction),
		Binding:   binding.fingerprint,
		ExpiresAt: c.now().Add(c.ttl).Unix(),
		Values:    values,
	})
	if err != nil {
		return "", fmt.Errorf("encoding cursor payload: %w", err)
	}

	key := c.ring.active
	tag := sign(key, TokenVersion, key.id, body)
	token := strings.Join([]string{
		TokenVersion,
		key.id,
		base64.RawURLEncoding.EncodeToString(body),
		base64.RawURLEncoding.EncodeToString(tag),
	}, ".")
	if len(token) > MaxTokenBytes {
		return "", fmt.Errorf("%w: encoded cursor is %d bytes, maximum is %d", ErrCursorValues, len(token), MaxTokenBytes)
	}
	return token, nil
}

// Decode verifies one cursor and returns its position.
//
// The checks run in the order an attacker's effort should be refused in:
// structure and version first, then the authentication tag, and only then the
// claims inside the payload. Nothing derived from an unverified payload
// influences the result.
func (c *Codec) Decode(binding Binding, token string) (Cursor, error) {
	if binding.IsZero() {
		return Cursor{}, ErrBinding
	}
	if token == "" || len(token) > MaxTokenBytes {
		return Cursor{}, fmt.Errorf("%w: length %d", ErrCursorMalformed, len(token))
	}

	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		return Cursor{}, fmt.Errorf("%w: %d dot-separated parts, want 4", ErrCursorMalformed, len(parts))
	}
	version, keyID, encodedBody, encodedTag := parts[0], parts[1], parts[2], parts[3]
	if version != TokenVersion {
		return Cursor{}, fmt.Errorf("%w: %q", ErrCursorVersion, version)
	}
	if !validKeyID(keyID) {
		return Cursor{}, fmt.Errorf("%w: key identifier", ErrCursorMalformed)
	}
	body, err := base64.RawURLEncoding.DecodeString(encodedBody)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: payload is not base64url", ErrCursorMalformed)
	}
	tag, err := base64.RawURLEncoding.DecodeString(encodedTag)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: tag is not base64url", ErrCursorMalformed)
	}

	key, known := c.ring.find(keyID)
	if !known || !hmac.Equal(tag, sign(key, version, keyID, body)) {
		return Cursor{}, ErrCursorSignature
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var decoded payload
	if err := decoder.Decode(&decoded); err != nil {
		return Cursor{}, fmt.Errorf("%w: payload is not the expected object", ErrCursorMalformed)
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return Cursor{}, fmt.Errorf("%w: payload holds more than one value", ErrCursorMalformed)
	}

	direction, err := ParseDirection(decoded.Direction)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: %w", ErrCursorMalformed, err)
	}
	if err := checkValues(decoded.Values); err != nil {
		return Cursor{}, fmt.Errorf("%w: %w", ErrCursorMalformed, err)
	}

	// Expiry is checked before the binding so that an old cursor reports the
	// reason a client can act on — retry from the first page — rather than a
	// filter mismatch it cannot do anything about.
	expiresAt := time.Unix(decoded.ExpiresAt, 0).UTC()
	if !c.now().Before(expiresAt) {
		return Cursor{}, fmt.Errorf("%w at %s", ErrCursorExpired, expiresAt.Format(time.RFC3339))
	}
	if decoded.Binding != binding.fingerprint {
		return Cursor{}, ErrCursorBinding
	}

	return Cursor{
		Direction: direction,
		Values:    slices.Clone(decoded.Values),
		ExpiresAt: expiresAt,
	}, nil
}

// sign computes the authentication tag over every field a decoder trusts.
//
// The inputs are length-prefixed rather than concatenated: without that,
// version "nc1" with key "a" and version "nc" with key "1a" would hash the
// same bytes, and a key identifier could be moved across a field boundary
// without invalidating the tag.
func sign(key Key, version, keyID string, body []byte) []byte {
	mac := hmac.New(sha256.New, key.secret)
	writeLengthPrefixed(mac, []byte(version))
	writeLengthPrefixed(mac, []byte(keyID))
	writeLengthPrefixed(mac, body)
	return mac.Sum(nil)
}

// checkValues holds sort-key components to this package's bounds. Values are
// required to be valid UTF-8 because they are re-encoded as JSON strings, and
// invalid bytes would silently become replacement characters on the way out.
func checkValues(values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("%w: a cursor carries at least one value", ErrCursorValues)
	}
	if len(values) > MaxCursorValues {
		return fmt.Errorf("%w: %d values, maximum is %d", ErrCursorValues, len(values), MaxCursorValues)
	}
	for _, value := range values {
		if len(value) > MaxCursorValueBytes {
			return fmt.Errorf("%w: value of %d bytes, maximum is %d", ErrCursorValues, len(value), MaxCursorValueBytes)
		}
		if !utf8.ValidString(value) {
			return fmt.Errorf("%w: value is not valid UTF-8", ErrCursorValues)
		}
	}
	return nil
}

func writeLengthPrefixed(w io.Writer, data []byte) {
	writeUint64(w, uint64(len(data)))
	// Writes go to a bytes.Buffer or an hmac.Hash; neither can fail, and
	// neither has a partial-write mode this code could recover from.
	_, _ = w.Write(data)
}

func writeUint64(w io.Writer, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	_, _ = w.Write(buf[:])
}
