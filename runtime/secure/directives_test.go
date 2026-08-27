package secure_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/secure"
)

// parseCSP splits a Content-Security-Policy header value into directive name
// and joined source list.
func parseCSP(value string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(value, ";") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		out[fields[0]] = strings.Join(fields[1:], " ")
	}
	return out
}

// diffCSP compares a parsed policy against the expected directive set and
// returns one problem per difference: a missing directive, an extra
// directive, or a directive whose sources changed.
//
// It is a function returning problems rather than an assertion on *testing.T
// so that TestDirectiveAssertionIsSensitive can check the assertion itself
// has teeth — a comparison that cannot fail is worse than no comparison,
// because it reads like coverage.
func diffCSP(got, want map[string]string) []string {
	var problems []string
	for name, wantSources := range want {
		gotSources, present := got[name]
		switch {
		case !present:
			problems = append(problems, fmt.Sprintf("directive %q is missing", name))
		case gotSources != wantSources:
			problems = append(problems,
				fmt.Sprintf("directive %q sources = %q, want %q", name, gotSources, wantSources))
		}
	}
	for name := range got {
		if _, expected := want[name]; !expected {
			problems = append(problems, fmt.Sprintf("unexpected directive %q", name))
		}
	}
	sort.Strings(problems)
	return problems
}

// wantDocumentDirectives is the document policy's directive set, written as
// a set rather than a string so that a dropped directive is reported by name.
func wantDocumentDirectives() map[string]string {
	return map[string]string{
		"default-src":     "'none'",
		"base-uri":        "'none'",
		"connect-src":     "'self'",
		"font-src":        "'self'",
		"form-action":     "'self'",
		"frame-ancestors": "'none'",
		"img-src":         "'self' data:",
		"object-src":      "'none'",
		"script-src":      "'self'",
		"style-src":       "'self'",
	}
}

func TestDocumentDirectiveSetIsExact(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	if problems := diffCSP(parseCSP(p.ContentSecurityPolicy()), wantDocumentDirectives()); len(problems) > 0 {
		t.Errorf("document CSP differs from the reviewed policy:\n  %s\n  header: %s",
			strings.Join(problems, "\n  "), p.ContentSecurityPolicy())
	}
}

// TestDirectiveAssertionIsSensitive is the test the brief asks for: one that
// fails if a directive is silently dropped.
//
// It does not test the policy. It tests TestDocumentDirectiveSetIsExact, by
// taking the real header, removing one directive at a time, and requiring
// that the comparison notices — by name. An assertion that passed on a policy
// with frame-ancestors missing would still look green in CI while the
// application had lost its clickjacking defense, and this is what rules that
// out.
func TestDirectiveAssertionIsSensitive(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	full := parseCSP(p.ContentSecurityPolicy())
	want := wantDocumentDirectives()

	if len(full) == 0 {
		t.Fatal("parsed no directives at all; the parser, not the policy, is broken")
	}

	for name := range want {
		t.Run("dropped/"+name, func(t *testing.T) {
			t.Parallel()
			mutated := map[string]string{}
			for k, v := range full {
				if k != name {
					mutated[k] = v
				}
			}
			problems := diffCSP(mutated, want)
			if len(problems) == 0 {
				t.Fatalf("dropping directive %q produced no complaint; the assertion has no teeth", name)
			}
			if !strings.Contains(strings.Join(problems, "\n"), name) {
				t.Errorf("dropping %q was reported as %v, which does not name the directive", name, problems)
			}
		})
	}

	for name, sources := range want {
		t.Run("weakened/"+name, func(t *testing.T) {
			t.Parallel()
			mutated := map[string]string{}
			for k, v := range full {
				mutated[k] = v
			}
			mutated[name] = sources + " https://attacker.example"
			if problems := diffCSP(mutated, want); len(problems) == 0 {
				t.Fatalf("widening directive %q produced no complaint; the assertion has no teeth", name)
			}
		})
	}

	t.Run("added", func(t *testing.T) {
		t.Parallel()
		mutated := map[string]string{}
		for k, v := range full {
			mutated[k] = v
		}
		mutated["worker-src"] = "'self'"
		if problems := diffCSP(mutated, want); len(problems) == 0 {
			t.Fatal("adding an unreviewed directive produced no complaint; the assertion has no teeth")
		}
	})
}

func TestDirectiveOrderingIsStable(t *testing.T) {
	t.Parallel()

	p, err := secure.NewDocumentPolicy(secure.Production)
	if err != nil {
		t.Fatalf("NewDocumentPolicy: %v", err)
	}
	value := p.ContentSecurityPolicy()

	if !strings.HasPrefix(value, "default-src 'none'; ") {
		t.Errorf("CSP does not start with default-src: %q", value)
	}
	names := []string{}
	for _, part := range strings.Split(value, ";") {
		fields := strings.Fields(part)
		if len(fields) > 0 {
			names = append(names, fields[0])
		}
	}
	rest := names[1:]
	if !sort.StringsAreSorted(rest) {
		t.Errorf("directives after default-src are not sorted: %v", rest)
	}
}
