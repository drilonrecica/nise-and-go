package cli

import "testing"

func TestLevenshtein(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"version", "version", 0},
		{"versoin", "version", 2}, //nolint:misspell // deliberate typo fixture, not a real misspelling
		{"docter", "doctor", 1},
		{"kitten", "sitting", 3},
		{"ping", "pign", 2},
	}
	for _, tt := range tests {
		t.Run(tt.a+"->"+tt.b, func(t *testing.T) {
			t.Parallel()
			if got := levenshtein(tt.a, tt.b); got != tt.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSuggest(t *testing.T) {
	t.Parallel()
	candidates := []string{"version", "doctor", "new", "dev", "check", "test", "generate"}

	tests := []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{"close typo matches", "versoin", "version", true}, //nolint:misspell // deliberate typo fixture, not a real misspelling
		{"single-letter swap matches", "doctro", "doctor", true},
		{"exact match", "dev", "dev", true},
		{"wildly different input has no suggestion", "zzzzzzzzzzzzzzz", "", false},
		{"empty input has no suggestion", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := suggest(tt.input, candidates)
			if ok != tt.ok {
				t.Fatalf("suggest(%q) ok = %v, want %v (got %q)", tt.input, ok, tt.ok, got)
			}
			if ok && got != tt.want {
				t.Errorf("suggest(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSuggestNoCandidates(t *testing.T) {
	t.Parallel()
	if _, ok := suggest("anything", nil); ok {
		t.Error("suggest with no candidates returned ok=true")
	}
}
