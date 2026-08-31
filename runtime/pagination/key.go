package pagination

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

// MinKeyBytes is the shortest signing secret a [Key] accepts. It matches
// HMAC-SHA256's block-independent full output width: a shorter secret reduces
// the work an offline forgery attempt has to do without making anything else
// cheaper.
const MinKeyBytes = 32

// MaxKeyIDBytes is the longest key identifier a [Key] accepts. Identifiers
// travel in every issued cursor, so they are bounded like any other wire
// field.
const MaxKeyIDBytes = 32

// MaxRetiredKeys is the largest number of verification-only keys a [KeyRing]
// accepts. Rotation needs one or two; a ring far larger than that is a
// configuration mistake, and every extra key is one more constant-time
// verification an unauthenticated request can force.
const MaxRetiredKeys = 8

// redactedKeyMaterial is what every formatting and serialization path on Key
// and KeyRing returns in place of secret material.
const redactedKeyMaterial = "[REDACTED]"

// Errors reported by key construction and parsing.
var (
	// ErrKeyID reports an identifier that is empty, too long, or uses a
	// character outside the permitted set.
	ErrKeyID = errors.New("cursor key identifier is not lowercase alphanumeric with - or _")
	// ErrKeySecret reports secret material shorter than [MinKeyBytes].
	ErrKeySecret = errors.New("cursor key secret is shorter than the minimum length")
	// ErrKeyEncoding reports a serialized key that is not "<id>:<base64 secret>".
	ErrKeyEncoding = errors.New("cursor key is not <id>:<base64 secret>")
	// ErrDuplicateKeyID reports two keys in one ring sharing an identifier.
	ErrDuplicateKeyID = errors.New("cursor key identifiers are not unique")
	// ErrTooManyRetiredKeys reports more than [MaxRetiredKeys] retired keys.
	ErrTooManyRetiredKeys = errors.New("cursor key ring holds too many retired keys")
)

// Key is one named HMAC-SHA256 signing secret.
//
// Its fields are unexported so that every value has passed [NewKey]'s
// validation, and so that the secret cannot reach a log line through struct
// formatting.
type Key struct {
	id     string
	secret []byte
}

// NewKey validates and returns one signing key. secret is copied, so a caller
// may reuse or zero its buffer afterwards.
func NewKey(id string, secret []byte) (Key, error) {
	if !validKeyID(id) {
		return Key{}, fmt.Errorf("%w: %q", ErrKeyID, id)
	}
	if len(secret) < MinKeyBytes {
		return Key{}, fmt.Errorf("%w: %d bytes, minimum is %d", ErrKeySecret, len(secret), MinKeyBytes)
	}
	return Key{id: id, secret: slices.Clone(secret)}, nil
}

// ParseKey reads one key from the "<id>:<base64 secret>" form used by
// configuration. The secret accepts both standard and raw (unpadded) base64.
func ParseKey(encoded string) (Key, error) {
	id, rawSecret, found := strings.Cut(encoded, ":")
	if !found || rawSecret == "" {
		return Key{}, ErrKeyEncoding
	}
	secret, err := decodeKeySecret(rawSecret)
	if err != nil {
		return Key{}, fmt.Errorf("%w: %w", ErrKeyEncoding, err)
	}
	return NewKey(id, secret)
}

// GenerateKey returns a key with the given identifier and a fresh
// cryptographically random secret. It is the supported way to obtain an
// ephemeral development key; a deployment that must issue cursors surviving a
// restart configures a key instead.
func GenerateKey(id string) (Key, error) {
	secret := make([]byte, MinKeyBytes)
	if _, err := rand.Read(secret); err != nil {
		return Key{}, fmt.Errorf("generating cursor key secret: %w", err)
	}
	return NewKey(id, secret)
}

// ID returns the key's identifier, which is public: it travels in the clear in
// every cursor so a decoder can select the verification key.
func (k Key) ID() string { return k.id }

// String implements [fmt.Stringer] with a fixed redaction placeholder so no
// formatting verb can print secret material.
func (k Key) String() string { return redactedKeyMaterial }

// GoString implements [fmt.GoStringer], the interface fmt consults for "%#v".
// It always returns the redaction placeholder.
func (k Key) GoString() string { return redactedKeyMaterial }

// LogValue implements [log/slog.LogValuer] so a key passed directly to a log
// call records only its public identifier.
func (k Key) LogValue() slog.Value { return slog.StringValue("key " + k.id) }

// MarshalJSON implements [encoding/json.Marshaler]. It always encodes the
// redaction placeholder, never the secret.
func (k Key) MarshalJSON() ([]byte, error) { return json.Marshal(redactedKeyMaterial) }

// KeyRing holds the one key that signs new cursors and the retired keys that
// still verify outstanding ones.
//
// The zero value is unusable; construct one with [NewKeyRing].
type KeyRing struct {
	active  Key
	retired []Key
}

// NewKeyRing returns a ring whose active key signs and whose retired keys only
// verify. Identifiers must be unique across the whole ring, because a
// duplicate would make cursor verification depend on ordering rather than on
// the identifier the token names.
func NewKeyRing(active Key, retired ...Key) (*KeyRing, error) {
	if active.id == "" {
		return nil, fmt.Errorf("%w: active key is not constructed", ErrKeyID)
	}
	if len(retired) > MaxRetiredKeys {
		return nil, fmt.Errorf("%w: %d, maximum is %d", ErrTooManyRetiredKeys, len(retired), MaxRetiredKeys)
	}
	seen := map[string]struct{}{active.id: {}}
	for _, key := range retired {
		if key.id == "" {
			return nil, fmt.Errorf("%w: retired key is not constructed", ErrKeyID)
		}
		if _, duplicate := seen[key.id]; duplicate {
			return nil, fmt.Errorf("%w: %q appears twice", ErrDuplicateKeyID, key.id)
		}
		seen[key.id] = struct{}{}
	}
	return &KeyRing{active: active, retired: slices.Clone(retired)}, nil
}

// ActiveID returns the identifier of the key new cursors are signed with.
func (r *KeyRing) ActiveID() string { return r.active.id }

// IDs returns every identifier the ring verifies, active key first, in the
// order verification would consider them.
func (r *KeyRing) IDs() []string {
	ids := make([]string, 0, 1+len(r.retired))
	ids = append(ids, r.active.id)
	for _, key := range r.retired {
		ids = append(ids, key.id)
	}
	return ids
}

// String implements [fmt.Stringer]. It names the ring's keys, never their
// secrets.
func (r *KeyRing) String() string {
	return "cursor key ring " + strings.Join(r.IDs(), ",")
}

// GoString implements [fmt.GoStringer] with the same redacted summary as
// [KeyRing.String].
func (r *KeyRing) GoString() string { return r.String() }

// LogValue implements [log/slog.LogValuer], recording the active identifier
// and the ring size rather than any key material.
func (r *KeyRing) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("active_key_id", r.active.id),
		slog.Int("retired_keys", len(r.retired)),
	)
}

// MarshalJSON implements [encoding/json.Marshaler]. It always encodes the
// redaction placeholder, never key material.
func (r *KeyRing) MarshalJSON() ([]byte, error) { return json.Marshal(redactedKeyMaterial) }

// find returns the key with the given identifier. The lookup is over public
// identifiers only; a miss is reported to the caller as a signature failure so
// that a probe cannot use the distinction to enumerate configured keys.
func (r *KeyRing) find(id string) (Key, bool) {
	if id == r.active.id {
		return r.active, true
	}
	for _, key := range r.retired {
		if id == key.id {
			return key, true
		}
	}
	return Key{}, false
}

// decodeKeySecret accepts standard base64 (what `openssl rand -base64 32`
// prints) and the raw unpadded form, so an operator does not have to know
// which variant the loader wanted.
func decodeKeySecret(raw string) ([]byte, error) {
	if secret, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return secret, nil
	}
	secret, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("secret is not base64")
	}
	return secret, nil
}

// validKeyID restricts identifiers to a set that is safe unencoded inside a
// dot-separated token and readable in a log line.
func validKeyID(id string) bool {
	if id == "" || len(id) > MaxKeyIDBytes {
		return false
	}
	for _, char := range id {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '-', char == '_':
		default:
			return false
		}
	}
	return true
}
