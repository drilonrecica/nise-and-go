package config

import (
	"strings"
	"testing"
)

func TestParseEnvironment(t *testing.T) {
	tests := []struct {
		raw     string
		want    Environment
		wantErr bool
	}{
		{"development", Development, false},
		{"test", Test, false},
		{"production", Production, false},
		{"", "", true},
		{"Production", "", true}, // case-sensitive: no silent normalization
		{"staging", "", true},
		{"prod", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := ParseEnvironment(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseEnvironment(%q) = %q, nil; want an error", tt.raw, got)
				}
				if !strings.Contains(err.Error(), tt.raw) {
					t.Fatalf("error %q does not mention the rejected value %q", err, tt.raw)
				}
				// lifecycle.ParseMode and secure.ParseDeployment both name
				// their package; this is the third parallel parser and it
				// used to be the one that did not. An unprefixed error
				// reaches an operator's startup log with no clue which of
				// several closed value sets rejected the value.
				if !strings.HasPrefix(err.Error(), "config: ") {
					t.Fatalf("error %q is not prefixed with its package name", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEnvironment(%q) unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParseEnvironment(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
