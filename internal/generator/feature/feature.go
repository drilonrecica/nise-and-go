// Package feature renders the vertical slice `nise generate feature` and
// `nise generate resource` produce.
//
// It creates files and modifies none. Where a slice needs a line in a file the
// application owns — the OpenAPI document, the constructor wiring, the API
// adapter, the navigation list — the command prints what to add and writes
// nothing. ADR 0026 records why: those files are application-owned, rewriting
// them discards the owner's work, marker-based insertion is what ADR 0009
// already refuses, and a generator that reformats somebody's contract while
// adding one path is a generator they stop running.
//
// A generated slice is application-owned from the moment it is written. Nise
// does not regenerate it, upgrade it, or reconcile it.
package feature

import (
	"errors"
	"fmt"
	"strings"
)

// Kind is what is being generated.
type Kind string

const (
	// KindFeature is a vertical-slice skeleton: domain, use case, SQL, and
	// tests, with no CRUD surface.
	KindFeature Kind = "feature"
	// KindResource is a feature plus the complete create/read/update/delete
	// surface: operations, queries, a migration, and the frontend flows.
	KindResource Kind = "resource"
)

// Names are every spelling of one feature's name, derived once.
//
// They are derived rather than asked for because a generator that took six
// spellings would be a generator whose output could disagree with itself. The
// rules are the ones docs/generated-application-layout.md already states:
// singular lowercase for the Go package and directory, plural for the REST
// collection, the table, and the route group.
type Names struct {
	// Singular is the canonical lowercase name: the feature directory, the Go
	// package, and the sqlc package's parent.
	Singular string
	// Plural is the REST collection, the table, and the route group.
	Plural string
	// Type is the exported Go type: the singular, capitalized.
	Type string
	// Collection is the exported Go type for a page of them.
	Collection string
	// Title is the singular for a person to read, capitalized.
	Title string
	// TitlePlural is the plural for a person to read, capitalized.
	TitlePlural string
	// Component is the PascalCase prefix for this feature's Svelte
	// components.
	Component string
}

// NewNames derives every spelling from the canonical singular.
//
// canonical must already have passed the CLI's own name validation: a single
// word, starting with a letter, letters and digits only, lowercased. This
// function refuses anything else rather than producing a tree named after it.
func NewNames(canonical string) (Names, error) {
	if canonical == "" {
		return Names{}, errors.New("feature: a name is required")
	}
	for i, r := range canonical {
		valid := (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9')
		if !valid {
			return Names{}, fmt.Errorf("feature: %q is not a canonical name; expected lowercase letters and digits, starting with a letter", canonical)
		}
	}
	title := strings.ToUpper(canonical[:1]) + canonical[1:]
	plural := Pluralize(canonical)
	return Names{
		Singular:    canonical,
		Plural:      plural,
		Type:        title,
		Collection:  title + "Collection",
		Title:       title,
		TitlePlural: strings.ToUpper(plural[:1]) + plural[1:],
		Component:   title,
	}, nil
}

// Pluralize applies English's regular plural rules, and only those.
//
// It is deliberately small. A generator that shipped an irregular-noun
// dictionary would be a generator that pluralized "person" as "people" and
// "status" as "statuses" and "datum" as "data", and would still be wrong for
// the domain word somebody actually typed — while making the wrongness harder
// to predict. Regular rules are wrong in exactly the cases a person can see
// coming, and the name is theirs to rename afterwards.
func Pluralize(singular string) string {
	switch {
	case singular == "":
		return ""
	// -s, -x, -z, -ch, -sh take -es: bus → buses, box → boxes, batch → batches.
	case strings.HasSuffix(singular, "s"),
		strings.HasSuffix(singular, "x"),
		strings.HasSuffix(singular, "z"),
		strings.HasSuffix(singular, "ch"),
		strings.HasSuffix(singular, "sh"):
		return singular + "es"
	// A consonant before -y becomes -ies: category → categories. A vowel does
	// not: day → days.
	case strings.HasSuffix(singular, "y") && len(singular) > 1 && !isVowel(singular[len(singular)-2]):
		return singular[:len(singular)-1] + "ies"
	default:
		return singular + "s"
	}
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	default:
		return false
	}
}
