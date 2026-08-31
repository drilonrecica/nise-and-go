package nonetwork

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// scanRoots are the trees this telemetry proof greps. templates/ is
// included because it is copied byte-for-byte into every generated
// application (internal/generator's whole job); a telemetry SDK or a
// stray tracked host planted there would ship in every project nise
// creates, not just in nise itself.
var scanRoots = []string{"cmd", "internal", "runtime", "templates"}

// telemetryMarkers are analytics/telemetry SDK names and hostnames that
// have no legitimate reason to appear anywhere in this repository's
// production source or generated output. Matching is case-insensitive and
// looks for the marker as a substring, deliberately: a vendored SDK's
// import path, a hostname, and a comment mentioning the product name are
// all equally disqualifying, and none of them appears anywhere in this
// tree today (see TestNoTelemetryMarkers, which fails this test the
// moment one does).
var telemetryMarkers = []string{
	"posthog",
	"segment.io",
	"segment.com",
	"sentry.io",
	"getsentry",
	"mixpanel",
	"google-analytics",
	"googletagmanager",
	"amplitude.com",
	"fullstory",
	"hotjar",
	"intercom.io",
	"heap.io",
	"bugsnag",
	"honeycomb.io",
	"datadoghq.com",
	"newrelic.com",
}

// httpsHostAllowlist is every hard-coded https:// host this repository's
// production source is permitted to mention. Every entry here is a
// documentation, specification, or install-instruction reference printed
// or linked for a human to read — never a URL any command fetches. See
// each command's own comment for why.
var httpsHostAllowlist = map[string]string{
	// The module path and its own GitHub repository: written into
	// generated files (AGENTS.md) and doc comments (ADR links), never
	// fetched.
	"github.com": "the module's own repository (module path, ADR doc-comment links, generated AGENTS.md content) — never fetched by nise",

	// doctor's remedy text for a missing or too-old local tool. Printed as
	// a plain string in an error message for a human to read and act on
	// by hand; nise never fetches these.
	"go.dev":          "doctor's remedy text pointing a developer at the Go download page",
	"nodejs.org":      "doctor's remedy text pointing a developer at the Node.js download page",
	"docs.docker.com": "doctor's remedy text pointing a developer at the Docker install docs",
	"podman.io":       "doctor's remedy text pointing a developer at the Podman install docs",

	// Specification and format references in doc comments.
	"no-color.org":  "doc comment citing the NO_COLOR informal standard",
	"prometheus.io": "doc comment citing the Prometheus exposition format spec",

	// Hosts that appear only inside templates/ — copied verbatim into
	// every generated application, never fetched by nise itself.
	"svelte.dev": "a doc-comment link in the generated app.d.ts, pointing a reader at SvelteKit's own type docs",
	"www.w3.org": "the SVG xmlns namespace URI in the generated placeholder favicon — an XML namespace identifier, not a network destination",
	"inlang.com": "the $schema identifiers in the generated inlang settings and message files — an editor hint naming a schema, never fetched by any build step; the message-format plugin itself is an ordinary pinned devDependency resolved from node_modules, deliberately not the CDN URL inlang's documentation uses",
}

// httpsHostPattern captures the host component of a hard-coded
// "https://host" or "http://host" literal appearing in Go source: a
// quoted or backtick-quoted string, or the bare text of a doc comment
// (both matter equally — a doc comment is still a hard-coded reference a
// reader could be misled by, and this test's job is to make every one of
// them a deliberate, allowlisted decision).
var httpsHostPattern = regexp.MustCompile(`https?://([A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+)`)

// reservedTLDs are the top-level domains IANA guarantees will never be
// delegated: RFC 2606 reserves .test, .example, .invalid, and .localhost
// exactly so that documentation, examples, and test fixtures have names that
// cannot resolve, and RFC 6761 gives them special-use status.
//
// A name under one of them is not a network destination and cannot become one,
// so it needs no per-host allowlist entry. Listing them individually would mean
// a growing list of fixture hostnames whose entries all say the same thing,
// which makes the real entries — the ones naming a host that does resolve —
// harder to see.
var reservedTLDs = map[string]bool{
	"test":      true,
	"example":   true,
	"invalid":   true,
	"localhost": true,
}

// reservedSecondLevel are the second-level names RFC 2606 reserves for the
// same purpose.
var reservedSecondLevel = map[string]bool{
	"example.com": true,
	"example.net": true,
	"example.org": true,
}

// isReservedDocumentationHost reports whether host is one IANA guarantees can
// never resolve.
//
// The suffix comparisons are anchored on a dot, deliberately: a bare
// strings.HasSuffix(host, "example") would let "not-an-example" through, and
// "example.com.evil-telemetry.net" must be reported like any other real host.
func isReservedDocumentationHost(host string) bool {
	if reservedSecondLevel[host] || reservedTLDs[host] {
		return true
	}
	for name := range reservedSecondLevel {
		if strings.HasSuffix(host, "."+name) {
			return true
		}
	}
	labels := strings.Split(host, ".")
	return len(labels) > 1 && reservedTLDs[labels[len(labels)-1]]
}

// loopbackHosts never need an allowlist entry: they are not network
// destinations outside this process, they are the localhost addresses
// nise dev's own proxy and health/lifecycle test fixtures bind or dial.
//
// Matched by exact string equality, deliberately — not
// strings.HasPrefix, which a prefix match like "127.0.0.1" would let
// "127.0.0.1.evil-telemetry.example" walk straight through as
// "loopback" and skip the allowlist check entirely. isLoopbackHost's
// only job is to recognize these four literals; anything merely
// starting with one of them is a real, unallowlisted host and must be
// reported like any other.
var loopbackHosts = map[string]bool{
	"127.0.0.1": true,
	"localhost": true,
	"0.0.0.0":   true,
	"::1":       true,
}

// scanExtensions are the file suffixes goSourceFiles walks. ".tmpl" is
// templates/'s own convention (internal/generator strips it verbatim when
// writing a generated file), so a template that will become
// app.d.ts or favicon.svg in every generated project is scanned in its
// on-disk form, not skipped for not literally ending in ".go".
var scanExtensions = []string{".go", ".tmpl"}

// TestNoTelemetryMarkers greps every non-test source file under scanRoots
// (see scanExtensions) for telemetryMarkers and for any hard-coded
// https://(or http://) host that is not in httpsHostAllowlist, failing
// with the exact file and line for each hit.
//
// _test.go files are excluded: runtime/secure's tests build CSP header
// values against fixture hosts like https://cdn.example and
// https://attacker.example, which are exactly the kind of string this
// check exists to catch in *shipped* code but are the deliberate subject
// under test in the CSP builder's own test suite, not a real destination.
func TestNoTelemetryMarkers(t *testing.T) {
	t.Parallel()

	files := goSourceFiles(t, scanRoots)
	if len(files) == 0 {
		t.Fatal("nonetwork: found no .go files under scanRoots; the scan is misconfigured")
	}

	var violations []string
	for _, path := range files {
		lines, err := readLines(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for i, line := range lines {
			lower := strings.ToLower(line)
			for _, marker := range telemetryMarkers {
				if strings.Contains(lower, marker) {
					violations = append(violations, fmt.Sprintf("%s:%d: forbidden telemetry marker %q: %s",
						path, i+1, marker, strings.TrimSpace(line)))
				}
			}
			for _, m := range httpsHostPattern.FindAllStringSubmatch(line, -1) {
				host := strings.ToLower(m[1])
				if isLoopbackHost(host) || isReservedDocumentationHost(host) {
					continue
				}
				if _, ok := httpsHostAllowlist[host]; ok {
					continue
				}
				violations = append(violations, fmt.Sprintf("%s:%d: hard-coded host %q is not in httpsHostAllowlist: %s",
					path, i+1, host, strings.TrimSpace(line)))
			}
		}
	}

	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// TestHTTPSHostAllowlistHasNoStaleEntries keeps httpsHostAllowlist honest:
// an entry that no longer matches anything in the tree is a claim nobody
// can verify by reading the source, so it must be removed.
func TestHTTPSHostAllowlistHasNoStaleEntries(t *testing.T) {
	t.Parallel()

	files := goSourceFiles(t, scanRoots)
	found := make(map[string]bool)
	for _, path := range files {
		lines, err := readLines(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for _, line := range lines {
			for _, m := range httpsHostPattern.FindAllStringSubmatch(line, -1) {
				found[strings.ToLower(m[1])] = true
			}
		}
	}
	for host := range httpsHostAllowlist {
		if !found[host] {
			t.Errorf("httpsHostAllowlist names %q, which does not appear anywhere under %s — remove the stale entry",
				host, strings.Join(scanRoots, ", "))
		}
	}
}

func isLoopbackHost(host string) bool {
	return loopbackHosts[host]
}

// goSourceFiles returns every non-test .go file under the given
// repository-root-relative directories, sorted, so violation output is
// deterministic (Global Constraint 5 applies to this test's own output,
// not just to what nise generates).
func goSourceFiles(t *testing.T, roots []string) []string {
	t.Helper()

	repoRoot := repoRoot(t)
	var files []string
	for _, root := range roots {
		full := filepath.Join(repoRoot, root)
		if _, err := os.Stat(full); os.IsNotExist(err) {
			continue
		}
		walkErr := filepath.WalkDir(full, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") || strings.Contains(filepath.ToSlash(path), "/testdata/") {
				return nil
			}
			for _, ext := range scanExtensions {
				if strings.HasSuffix(path, ext) {
					files = append(files, path)
					return nil
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walking %s: %v", full, walkErr)
		}
	}
	sort.Strings(files)
	return files
}

// readLines reads path as a slice of lines without their trailing
// newlines.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path) // #nosec G304 -- path comes from this test's own filepath.WalkDir over the repository, never external input.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// TestReservedDocumentationHosts pins the anchoring the exemption depends on.
// A suffix match that was not anchored on a dot would let a real host that
// merely ends in a reserved name skip the allowlist entirely, which is the one
// way this exemption could become a hole.
func TestReservedDocumentationHosts(t *testing.T) {
	t.Parallel()

	reserved := []string{
		"example.com", "example.net", "example.org",
		"www.example.com", "app.example", "other.app.example",
		"something.test", "host.invalid", "api.localhost",
		"test", "example", "invalid", "localhost",
	}
	for _, host := range reserved {
		if !isReservedDocumentationHost(host) {
			t.Errorf("%q is not recognized as a reserved documentation host", host)
		}
	}

	real := []string{
		"example.com.evil-telemetry.net",
		"notexample.com",
		"examplecom",
		"my-example",
		"testing.io",
		"localhost.evil.io",
		"github.com",
		"",
	}
	for _, host := range real {
		if isReservedDocumentationHost(host) {
			t.Errorf("%q was treated as a reserved documentation host", host)
		}
	}
}
