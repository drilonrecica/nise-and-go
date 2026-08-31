package password

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Bounds on a compromised-password list.
const (
	// MaxCompromisedEntries bounds how many entries one list may hold. A
	// deployment that wants a full breach corpus should use a data structure
	// built for it, not an in-memory set of tens of millions of strings.
	MaxCompromisedEntries = 5_000_000
	// MaxCompromisedEntryBytes bounds one entry.
	MaxCompromisedEntryBytes = MaxPasswordBytes
)

// ErrCompromisedList reports an unusable compromised-password list.
var ErrCompromisedList = errors.New("compromised-password list is not usable")

// Compromised reports whether a password is known to have been breached.
//
// It is an interface because the honest answer depends on a decision this
// project will not make for an application owner. Nise ships exactly one
// implementation, [CommonList], which is local and offline; an owner who wants
// a full breach corpus supplies their own, and with it the privacy decision
// that comes from sending anything about a password anywhere.
//
// It takes a context because a caller's implementation may do I/O. The shipped
// one does not.
type Compromised interface {
	// IsCompromised reports whether password appears in the source.
	//
	// An error means the source could not answer, which is different from
	// answering "no". A caller must not treat the two the same: failing open
	// on an unavailable checker turns an outage into an accepted weak
	// password.
	IsCompromised(ctx context.Context, password string) (bool, error)
}

// CommonList is an in-memory set of known-compromised passwords.
//
// Membership is tested on a case-folded form, because an attacker's list
// contains "Password1" the moment it contains "password1", and a policy that
// refused one and accepted the other would be theatre.
type CommonList struct {
	entries map[string]struct{}
}

// NewCommonList builds a list from entries.
//
// Blank lines and lines beginning with "#" are ignored, so a caller can hand it
// a commented file verbatim. Entries are folded the same way lookups are.
func NewCommonList(entries []string) (*CommonList, error) {
	if len(entries) > MaxCompromisedEntries {
		return nil, fmt.Errorf("%w: %d entries, maximum is %d", ErrCompromisedList, len(entries), MaxCompromisedEntries)
	}
	held := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(trimmed) > MaxCompromisedEntryBytes {
			return nil, fmt.Errorf("%w: an entry is %d bytes, maximum is %d", ErrCompromisedList, len(trimmed), MaxCompromisedEntryBytes)
		}
		held[foldPassword(trimmed)] = struct{}{}
	}
	if len(held) == 0 {
		return nil, fmt.Errorf("%w: no entries", ErrCompromisedList)
	}
	return &CommonList{entries: held}, nil
}

// ParseCommonList reads a newline-separated list, as a file provides it.
func ParseCommonList(contents string) (*CommonList, error) {
	scanner := bufio.NewScanner(strings.NewReader(contents))
	scanner.Buffer(make([]byte, 0, 4096), MaxCompromisedEntryBytes+1)
	entries := make([]string, 0, 512)
	for scanner.Scan() {
		entries = append(entries, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompromisedList, err)
	}
	return NewCommonList(entries)
}

//go:embed common_passwords.txt
var builtinCommonPasswords string

var (
	builtinOnce sync.Once
	builtinList *CommonList
	builtinErr  error
)

// BuiltinCommonList returns the small list this package ships.
//
// It is deliberately small, and deliberately not a substitute for a real breach
// corpus: it holds the passwords that appear at the top of every
// credential-stuffing list, and nothing else. Its purpose is that a generated
// application refuses those without a network call, a multi-gigabyte download,
// or a privacy decision made on an owner's behalf.
//
// An application that needs more supplies its own [Compromised]. See
// docs/adr/0020-compromised-password-source.md for what each choice costs.
func BuiltinCommonList() (*CommonList, error) {
	builtinOnce.Do(func() {
		builtinList, builtinErr = ParseCommonList(builtinCommonPasswords)
	})
	return builtinList, builtinErr
}

// Len returns how many distinct entries the list holds.
func (l *CommonList) Len() int { return len(l.entries) }

// IsCompromised reports whether password is in the list. It never returns an
// error: the list is in memory and cannot fail to answer.
func (l *CommonList) IsCompromised(_ context.Context, password string) (bool, error) {
	_, found := l.entries[foldPassword(password)]
	return found, nil
}

// foldPassword normalizes a candidate for membership testing.
//
// Only case and surrounding whitespace are folded. Nothing else: stripping
// digits or punctuation would make "p@ssw0rd!" match "password", which is a
// different and much stronger claim than this list can support, and would
// refuse passwords that are genuinely fine.
func foldPassword(password string) string {
	return strings.ToLower(strings.TrimSpace(password))
}
