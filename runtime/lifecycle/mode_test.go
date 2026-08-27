package lifecycle

import "testing"

func TestParseMode(t *testing.T) {
	tests := []struct {
		raw     string
		want    Mode
		wantErr bool
	}{
		{"all", ModeAll, false},
		{"web", ModeWeb, false},
		{"worker", ModeWorker, false},
		{"", "", true},
		{"All", "", true},
		{"WORKER", "", true},
		{"both", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := ParseMode(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseMode(%q) = %q, nil, want an error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMode(%q) unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("ParseMode(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
