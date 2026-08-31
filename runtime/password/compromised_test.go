package password_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/password"
)

func TestBuiltinCommonListRefusesTheObviousOnes(t *testing.T) {
	t.Parallel()

	list, err := password.BuiltinCommonList()
	if err != nil {
		t.Fatalf("BuiltinCommonList: %v", err)
	}
	if list.Len() < 100 {
		t.Fatalf("the built-in list holds %d entries; it is meant to cover the top of every stuffing list", list.Len())
	}

	compromised := []string{
		"123456", "password", "qwerty", "letmein", "admin",
		"iloveyou", "welcome", "monkey", "dragon", "changeme",
		// Case is folded, because an attacker's list contains "Password1"
		// the moment it contains "password1".
		"PASSWORD", "Qwerty", "LetMeIn", " password ",
	}
	for _, candidate := range compromised {
		found, err := list.IsCompromised(context.Background(), candidate)
		if err != nil {
			t.Fatalf("IsCompromised(%q): %v", candidate, err)
		}
		if !found {
			t.Errorf("%q is not in the built-in list", candidate)
		}
	}

	// Nothing else is refused. A list that folded punctuation or digits away
	// would refuse passwords that are genuinely fine, and would make a much
	// stronger claim than it can support.
	acceptable := []string{
		"correct horse battery staple",
		"a-password-nobody-has-used",
		"p@ssw0rd!extra-entropy-here",
		"", "  ",
	}
	for _, candidate := range acceptable {
		found, err := list.IsCompromised(context.Background(), candidate)
		if err != nil {
			t.Fatalf("IsCompromised(%q): %v", candidate, err)
		}
		if found {
			t.Errorf("%q was refused by the built-in list", candidate)
		}
	}
}

func TestBuiltinCommonListIsStable(t *testing.T) {
	t.Parallel()

	first, err := password.BuiltinCommonList()
	if err != nil {
		t.Fatalf("BuiltinCommonList: %v", err)
	}
	second, err := password.BuiltinCommonList()
	if err != nil {
		t.Fatalf("BuiltinCommonList: %v", err)
	}
	if first != second || first.Len() != second.Len() {
		t.Fatal("the built-in list is rebuilt on every call")
	}
}

func TestParseCommonListIgnoresCommentsAndBlanks(t *testing.T) {
	t.Parallel()

	list, err := password.ParseCommonList("# a comment\n\nhunter2\n  spaced  \n# another\nHUNTER2\n")
	if err != nil {
		t.Fatalf("ParseCommonList: %v", err)
	}
	// "hunter2" and "HUNTER2" fold to one entry; the comment and blank line
	// contribute none.
	if list.Len() != 2 {
		t.Fatalf("Len = %d, want 2", list.Len())
	}
	for _, candidate := range []string{"hunter2", "Hunter2", "spaced"} {
		found, err := list.IsCompromised(context.Background(), candidate)
		if err != nil || !found {
			t.Errorf("IsCompromised(%q) = %t, %v", candidate, found, err)
		}
	}
	if found, _ := list.IsCompromised(context.Background(), "# a comment"); found {
		t.Error("a comment line became an entry")
	}
}

func TestNewCommonListRefusesUnusableInput(t *testing.T) {
	t.Parallel()

	if _, err := password.NewCommonList(nil); !errors.Is(err, password.ErrCompromisedList) {
		t.Errorf("an empty list was accepted: %v", err)
	}
	if _, err := password.NewCommonList([]string{"# only a comment", "  "}); !errors.Is(err, password.ErrCompromisedList) {
		t.Errorf("a list with no entries was accepted: %v", err)
	}
	oversized := strings.Repeat("x", password.MaxCompromisedEntryBytes+1)
	if _, err := password.NewCommonList([]string{oversized}); !errors.Is(err, password.ErrCompromisedList) {
		t.Errorf("an oversized entry was accepted: %v", err)
	}

	list, err := password.NewCommonList([]string{"one", "two", "one"})
	if err != nil {
		t.Fatalf("NewCommonList: %v", err)
	}
	if list.Len() != 2 {
		t.Errorf("Len = %d; duplicates should collapse", list.Len())
	}
}

// TestCompromisedInterfaceIsSatisfied pins that the shipped list is usable
// wherever the interface is required, so an application can replace it without
// changing any call site.
func TestCompromisedInterfaceIsSatisfied(t *testing.T) {
	t.Parallel()

	list, err := password.BuiltinCommonList()
	if err != nil {
		t.Fatalf("BuiltinCommonList: %v", err)
	}
	var checker password.Compromised = list
	found, err := checker.IsCompromised(context.Background(), "password")
	if err != nil || !found {
		t.Fatalf("through the interface: %t, %v", found, err)
	}
}
