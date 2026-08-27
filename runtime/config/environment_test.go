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
