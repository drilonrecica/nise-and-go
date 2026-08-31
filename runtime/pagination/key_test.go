package pagination_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/pagination"
)

func TestNewKeyValidatesIdentifierAndSecret(t *testing.T) {
	t.Parallel()

	good := make([]byte, pagination.MinKeyBytes)
	tests := []struct {
		name   string
		id     string
		secret []byte
		want   error
	}{
		{name: "empty identifier", id: "", secret: good, want: pagination.ErrKeyID},
		{name: "uppercase identifier", id: "Active", secret: good, want: pagination.ErrKeyID},
		{name: "dot in identifier", id: "a.b", secret: good, want: pagination.ErrKeyID},
		{name: "identifier too long", id: strings.Repeat("a", pagination.MaxKeyIDBytes+1), secret: good, want: pagination.ErrKeyID},
		{name: "secret too short", id: "active", secret: make([]byte, pagination.MinKeyBytes-1), want: pagination.ErrKeySecret},
		{name: "no secret", id: "active", secret: nil, want: pagination.ErrKeySecret},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := pagination.NewKey(tc.id, tc.secret); !errors.Is(err, tc.want) {
				t.Fatalf("NewKey error = %v, want %v", err, tc.want)
			}
		})
	}

	key, err := pagination.NewKey("y2026a-1_x", good)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	if key.ID() != "y2026a-1_x" {
		t.Errorf("ID = %q", key.ID())
	}
}

func TestNewKeyCopiesItsSecret(t *testing.T) {
	t.Parallel()

	secret := make([]byte, pagination.MinKeyBytes)
	for i := range secret {
		secret[i] = 0x11
	}
	key, err := pagination.NewKey("active", secret)
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
	binding := testBinding()
	token, err := codec.Encode(binding, pagination.Forward, []string{"1"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	for i := range secret {
		secret[i] = 0
	}
	if _, err := codec.Decode(binding, token); err != nil {
		t.Fatalf("zeroing the caller's buffer invalidated the key: %v", err)
	}
}

func TestParseKey(t *testing.T) {
	t.Parallel()

	secret := make([]byte, pagination.MinKeyBytes)
	for i := range secret {
		secret[i] = byte(i)
	}
	padded := base64.StdEncoding.EncodeToString(secret)
	raw := base64.RawStdEncoding.EncodeToString(secret)

	for _, encoded := range []string{"active:" + padded, "active:" + raw} {
		key, err := pagination.ParseKey(encoded)
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", encoded, err)
		}
		if key.ID() != "active" {
			t.Errorf("ID = %q, want active", key.ID())
		}
	}

	tests := []struct {
		name    string
		encoded string
		want    error
	}{
		{name: "no separator", encoded: "activesecret", want: pagination.ErrKeyEncoding},
		{name: "no secret", encoded: "active:", want: pagination.ErrKeyEncoding},
		{name: "secret is not base64", encoded: "active:not base64!", want: pagination.ErrKeyEncoding},
		{name: "empty", encoded: "", want: pagination.ErrKeyEncoding},
		{name: "identifier is rejected", encoded: "Active:" + padded, want: pagination.ErrKeyID},
		{name: "secret is too short", encoded: "active:" + base64.StdEncoding.EncodeToString(secret[:8]), want: pagination.ErrKeySecret},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := pagination.ParseKey(tc.encoded); !errors.Is(err, tc.want) {
				t.Fatalf("ParseKey error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestGenerateKeyProducesDistinctUsableSecrets(t *testing.T) {
	t.Parallel()

	binding := testBinding()
	first, err := pagination.GenerateKey("ephemeral")
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	second, err := pagination.GenerateKey("ephemeral")
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	codec := func(key pagination.Key) *pagination.Codec {
		ring, err := pagination.NewKeyRing(key)
		if err != nil {
			t.Fatalf("NewKeyRing: %v", err)
		}
		c, err := pagination.NewCodec(ring, time.Hour)
		if err != nil {
			t.Fatalf("NewCodec: %v", err)
		}
		return c
	}

	token, err := codec(first).Encode(binding, pagination.Forward, []string{"1"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := codec(second).Decode(binding, token); !errors.Is(err, pagination.ErrCursorSignature) {
		t.Fatalf("two generated keys verify each other's cursors: %v", err)
	}
	if _, err := pagination.GenerateKey("BAD ID"); !errors.Is(err, pagination.ErrKeyID) {
		t.Errorf("GenerateKey accepted an invalid identifier: %v", err)
	}
}

func TestNewKeyRingRejectsAmbiguousRings(t *testing.T) {
	t.Parallel()

	active := testKey(t, "active")
	if _, err := pagination.NewKeyRing(pagination.Key{}); !errors.Is(err, pagination.ErrKeyID) {
		t.Errorf("NewKeyRing accepted a zero active key: %v", err)
	}
	if _, err := pagination.NewKeyRing(active, pagination.Key{}); !errors.Is(err, pagination.ErrKeyID) {
		t.Errorf("NewKeyRing accepted a zero retired key: %v", err)
	}
	if _, err := pagination.NewKeyRing(active, testKey(t, "active")); !errors.Is(err, pagination.ErrDuplicateKeyID) {
		t.Errorf("NewKeyRing accepted the active identifier twice: %v", err)
	}
	if _, err := pagination.NewKeyRing(active, testKey(t, "old"), testKey(t, "old")); !errors.Is(err, pagination.ErrDuplicateKeyID) {
		t.Errorf("NewKeyRing accepted a repeated retired identifier: %v", err)
	}

	tooMany := make([]pagination.Key, 0, pagination.MaxRetiredKeys+1)
	for i := 0; i <= pagination.MaxRetiredKeys; i++ {
		tooMany = append(tooMany, testKey(t, fmt.Sprintf("r%d", i)))
	}
	if _, err := pagination.NewKeyRing(active, tooMany...); !errors.Is(err, pagination.ErrTooManyRetiredKeys) {
		t.Errorf("NewKeyRing accepted %d retired keys: %v", len(tooMany), err)
	}

	ring, err := pagination.NewKeyRing(active, testKey(t, "old"))
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	if got := ring.IDs(); len(got) != 2 || got[0] != "active" || got[1] != "old" {
		t.Errorf("IDs = %v, want [active old]", got)
	}
}

func TestKeyMaterialNeverReachesOutput(t *testing.T) {
	t.Parallel()

	const marker = "SECRETMATERIALSECRETMATERIALSECR"
	key, err := pagination.NewKey("active", []byte(marker))
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	ring, err := pagination.NewKeyRing(key, testKey(t, "old"))
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}

	rendered := []string{
		fmt.Sprintf("%v %s %q %#v", key, key, key, key),
		fmt.Sprintf("%v %s %q %#v", ring, ring, ring, ring),
		fmt.Sprint(slog.AnyValue(key).Resolve()),
		fmt.Sprint(slog.AnyValue(ring).Resolve()),
	}
	for _, encoder := range []any{key, ring} {
		encoded, err := json.Marshal(encoder)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		rendered = append(rendered, string(encoded))
	}
	for _, output := range rendered {
		if strings.Contains(output, marker) {
			t.Errorf("output exposed key material: %q", output)
		}
	}

	// The public identifier is deliberately not redacted: it is what makes a
	// rotation observable in a log.
	if !strings.Contains(ring.String(), "active,old") {
		t.Errorf("KeyRing.String = %q, want the public identifiers", ring.String())
	}
	if !strings.Contains(fmt.Sprint(slog.AnyValue(key).Resolve()), "active") {
		t.Errorf("Key.LogValue = %v, want the public identifier", slog.AnyValue(key).Resolve())
	}
}
