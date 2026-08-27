package clierr

import "testing"

func TestIsDeniedKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want bool
	}{
		{"password", true},
		{"db_password", true},
		{"DB-PASSWORD", true},
		{"api_key", true}, // separators are stripped before matching, so this normalizes to "apikey"
		{"apikey", true},
		{"Authorization", true},
		{"cookie", true},
		{"session_token", true},
		{"credential", true},
		{"private_key", true}, // normalizes to "privatekey"
		{"privatekey", true},
		{"attempt", false},
		{"dsn", false},
		{"count", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()
			if got := isDeniedKey(tt.key); got != tt.want {
				t.Errorf("isDeniedKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestRedactValue(t *testing.T) {
	t.Parallel()
	if got := redactValue("password", "hunter2"); got != RedactPlaceholder {
		t.Errorf("redactValue(password, ...) = %q, want %q", got, RedactPlaceholder)
	}
	if got := redactValue("attempt", "3"); got != "3" {
		t.Errorf("redactValue(attempt, ...) = %q, want unchanged", got)
	}
}

func TestScrubChainText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "postgres URL with credentials",
			in:   "dial postgres://alice:s3cr3t@db.internal:5432/app: refused",
			want: "dial postgres://" + RedactPlaceholder + "@db.internal:5432/app: refused",
		},
		{
			name: "no URL present",
			in:   "context deadline exceeded",
			want: "context deadline exceeded",
		},
		{
			name: "URL without credentials is untouched",
			in:   "dial postgres://db.internal:5432/app: refused",
			want: "dial postgres://db.internal:5432/app: refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := scrubChainText(tt.in); got != tt.want {
				t.Errorf("scrubChainText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
