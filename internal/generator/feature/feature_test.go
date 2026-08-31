package feature_test

import (
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator/feature"
)

func TestNewNamesDerivesEverySpelling(t *testing.T) {
	t.Parallel()

	names, err := feature.NewNames("invoice")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}
	want := feature.Names{
		Singular:    "invoice",
		Plural:      "invoices",
		Type:        "Invoice",
		Collection:  "InvoiceCollection",
		Title:       "Invoice",
		TitlePlural: "Invoices",
		Component:   "Invoice",
	}
	if names != want {
		t.Errorf("NewNames(invoice) = %#v, want %#v", names, want)
	}
}

// A generator that took six spellings would be a generator whose output could
// disagree with itself, so every spelling comes from one canonical name — and
// anything that is not already canonical is refused rather than turned into a
// directory named after it.
func TestNewNamesRefusesAnythingNotCanonical(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"", "Invoice", "invoice_line", "invoice-line", "1invoice", "invoice line", "faktúra"} {
		if _, err := feature.NewNames(bad); err == nil {
			t.Errorf("NewNames(%q) was accepted", bad)
		}
	}
}

func TestPluralizeAppliesTheRegularRulesOnly(t *testing.T) {
	t.Parallel()

	for singular, plural := range map[string]string{
		"invoice":  "invoices",
		"widget":   "widgets",
		"bus":      "buses",
		"box":      "boxes",
		"batch":    "batches",
		"dish":     "dishes",
		"category": "categories",
		"day":      "days",
		"key":      "keys",
		"status":   "statuses",
	} {
		if got := feature.Pluralize(singular); got != plural {
			t.Errorf("Pluralize(%q) = %q, want %q", singular, got, plural)
		}
	}
}

// An irregular-noun dictionary would be wrong in cases a person cannot predict
// and would still be wrong for the domain word they typed. The regular rules
// are wrong in exactly the cases somebody can see coming, and the name is
// theirs to rename afterwards.
func TestPluralizeDoesNotKnowIrregularNouns(t *testing.T) {
	t.Parallel()

	for singular, regular := range map[string]string{
		"person": "persons",
		"datum":  "datums",
		"child":  "childs",
	} {
		if got := feature.Pluralize(singular); got != regular {
			t.Errorf("Pluralize(%q) = %q, want the regular %q", singular, got, regular)
		}
	}
}
