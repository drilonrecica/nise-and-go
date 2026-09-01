package nonetwork

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

// modulePath is this repository's module path (Global Constraint 1),
// reused from internal/generator rather than re-declared, so this test
// cannot silently drift from the one place that literal is defined.
const modulePath = generator.NiseModulePath

// goPackage is the subset of `go list -json`'s Package struct this test
// needs. Imports is the package's own direct, non-test import list. Deps
// is the full transitive closure (excluding test-only imports), used only
// by the narrow, named exception below — see its doc comment.
type goPackage struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

// listPackages runs `go list -json` over patterns and decodes the
// concatenated stream of JSON objects it prints (one per package, with no
// separating commas or enclosing array — go list's documented format).
// Using os/exec plus encoding/json, per the task brief, rather than
// go/packages, which would add a dependency this module does not have.
func listPackages(t *testing.T, patterns ...string) []goPackage {
	t.Helper()

	args := append([]string{"list", "-json"}, patterns...)
	cmd := exec.Command("go", args...) // #nosec G204 -- args are fixed test-internal patterns, never user input.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}

	var pkgs []goPackage
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var p goPackage
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decode `go list -json` output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatalf("go %s: matched no packages", strings.Join(args, " "))
	}
	return pkgs
}

// forbiddenNetworkImports are the standard library import paths whose
// presence in a package's Imports means that package can dial out, listen,
// or otherwise perform network I/O on its own — the "net, or similar" the
// task brief asks for, made concrete. net/url is deliberately excluded:
// parsing a URL touches no socket.
var forbiddenNetworkImports = []string{
	"net",
	"net/http",
	"net/http/cgi",
	"net/http/httputil",
	"net/rpc",
	"net/smtp",
	"net/textproto",
	"crypto/tls",
}

// networkAllowlist is every package under ./cmd/... or ./internal/... that
// may directly import one of forbiddenNetworkImports, and why. It is
// checked for completeness within TestStaticNoUnexpectedNetworkImports, so
// an entry that stops being necessary is a required edit, not leftover.
//
// Keep this short. A new entry here is a claim that
// TestDynamicNoOutboundConnections (dynamic_test.go) actually exercises
// the command the entry names and finds zero connection attempts — the
// dynamic proof is what makes the claim true, not this comment.
var networkAllowlist = map[string]string{
	modulePath + "/internal/dev": "implements `nise dev` (docs/commands/dev.md): a loopback " +
		"reverse proxy (net/http/httputil) fronting the application and Vite dev servers, plus " +
		"TCP port probing before binding. This is the explicit, user-invoked purpose of the dev " +
		"command, and it is the one command this test suite deliberately does not run non-" +
		"interactively (it requires a generated project and a running frontend toolchain).",

	modulePath + "/internal/release": "implements the explicit update check behind `nise version " +
		"check` (docs/cli-and-distribution.md): one GET to GitHub's releases/latest endpoint, made " +
		"only inside a command a person typed. This is the one documented exception to " +
		"docs/no-telemetry.md's \"no command performs an implicit network request\" — implicit being " +
		"the operative word. It is a separate package precisely so this entry points at a file whose " +
		"whole purpose is that one request, rather than at every command nise has: the request " +
		"carries a constant User-Agent with no version, no authentication, no cookies, and follows " +
		"no redirect, and TestDynamicTheUpdateCheckIsTheOneNetworkCommand asserts it reaches exactly " +
		"one host while every other command reaches none.",

	modulePath + "/internal/cli": "package cli holds every command's registry entry in one Go " +
		"package (registry.go's own doc comment: \"the one place a task adds a command\"). " +
		"dev.go — nise dev's flag wiring and the loopback listener/http.Server it builds before " +
		"handing requests to internal/dev's proxy — lives in this package, which is why the " +
		"package as a whole, not just dev.go, must be allowlisted: Go compiles per package, not " +
		"per file. TestDynamicNoOutboundConnections proves every *other* command built from this " +
		"same package opens no socket.",
}

// transitiveNetworkException grants one package under ./internal/...
// permission to transitively depend on a *named, closed* set of
// network-capable packages under ./runtime/..., without that dependent
// package needing to import net/http itself.
//
// This is deliberately narrower than adding the dependent package to
// networkAllowlist (which would accept *any* direct network import from
// it) or exempting all of runtime/ (which would accept network-capable
// code reached through *any* runtime/ package, present or future — the
// blanket allowlist the task brief warns would gut this test). Naming the
// exact runtime/ packages means a new network-capable dependency reached
// through any other path, including a wider set of runtime/ packages than
// named here, still fails TestStaticNoUnexpectedNetworkImports.
type transitiveNetworkException struct {
	// runtimeDeps is the exact set of ./runtime/... import paths this
	// exception permits reaching net/http or net through. Listing a
	// package here that is not itself network-capable (runtime/config,
	// below) is fine — it only documents that the dependent package is
	// also allowed to depend on it; TestStaticNoUnexpectedNetworkImports
	// only ever flags a *widening* of the network-capable subset.
	runtimeDeps []string
	reason      string
}

// transitiveNetworkAllowlist holds the one such exception this codebase
// currently needs. See TestStaticNoUnexpectedNetworkImports for how it is
// enforced and TestStaticTransitiveNetworkAllowlistHasNoStaleEntries for
// how it is kept honest.
var transitiveNetworkAllowlist = map[string]transitiveNetworkException{
	modulePath + "/internal/check": {
		runtimeDeps: []string{
			modulePath + "/runtime/config",
			modulePath + "/runtime/lifecycle",
			modulePath + "/runtime/health",
		},
		reason: "internal/check/config.go calls runtime/config.ParseEnvironment and " +
			"runtime/lifecycle.ParseMode so `nise check` validates an environment file against the " +
			"exact closed sets the generated application enforces at startup — reimplementing that " +
			"parsing here would drift the moment the runtime's accepted values changed and this " +
			"check's copy did not. It calls only those two pure functions: it never constructs an " +
			"http.Server, never calls HTTPServer.Run, and never imports net or net/http itself. " +
			"runtime/lifecycle and runtime/health do contain HTTP *server* construction (an " +
			"http.Server, a reverse-proxy-shaped listener, and probe handlers) that a generated " +
			"application runs when it explicitly builds and starts one — that is their entire " +
			"published purpose (runtime-packages.md) — but this import graph links that code without " +
			"ever invoking it. It also adds no network-capable standard library package the CLI did " +
			"not already link: net/http and crypto/tls were already part of this binary's dependency " +
			"graph through internal/dev's reverse proxy before internal/check existed.",
	},
}

// TestStaticNoUnexpectedNetworkImports is the static proof: no package
// under ./cmd/... or ./internal/... may directly import a network-dialing
// standard library package, or transitively reach one through an
// unlisted path, unless it is named, with a reason, in networkAllowlist or
// transitiveNetworkAllowlist.
func TestStaticNoUnexpectedNetworkImports(t *testing.T) {
	t.Parallel()

	pkgs := listPackages(t, modulePath+"/cmd/...", modulePath+"/internal/...")
	runtimePkgs := listPackages(t, modulePath+"/runtime/...")

	// runtimeNetworkSources is every ./runtime/... package whose own
	// direct Imports contain a forbidden network import — the set
	// transitiveNetworkAllowlist entries are actually granting access to.
	//
	// runtimeDeps is each ./runtime/... package's own transitive Deps, so
	// a cmd/internal package that directly imports e.g. runtime/lifecycle
	// (which does not itself need to import runtime/health for
	// TestStaticRuntimeNeverImportsInternal's sake, but does, to build an
	// HTTPServer around a health.Gate) is charged for reaching
	// runtime/health too, without this test having to walk the whole
	// standard-library dependency graph by hand.
	runtimeNetworkSources := make(map[string]bool)
	runtimeDeps := make(map[string][]string, len(runtimePkgs))
	for _, pkg := range runtimePkgs {
		if len(importsAny(pkg.Imports, forbiddenNetworkImports)) > 0 {
			runtimeNetworkSources[pkg.ImportPath] = true
		}
		runtimeDeps[pkg.ImportPath] = pkg.Deps
	}

	directSeen := make(map[string]bool)
	transitiveSeen := make(map[string]bool)
	runtimePrefix := modulePath + "/runtime/"

	for _, pkg := range pkgs {
		// Direct check: does this package itself import a forbidden
		// package?
		if forbidden := importsAny(pkg.Imports, forbiddenNetworkImports); len(forbidden) > 0 {
			reason, allowed := networkAllowlist[pkg.ImportPath]
			switch {
			case !allowed:
				t.Errorf("%s directly imports %s, a network-dialing package, and is not in networkAllowlist (static_test.go)",
					pkg.ImportPath, strings.Join(forbidden, ", "))
			case reason == "":
				t.Errorf("%s is in networkAllowlist with no reason given", pkg.ImportPath)
			default:
				directSeen[pkg.ImportPath] = true
			}
		}

		// Transitive check: does this package *directly* import a
		// ./runtime/... package that is itself (or itself transitively
		// depends on) a network source? Scoping this to pkg's own direct
		// Imports — not pkg's full Deps closure — is what keeps this from
		// also firing for internal/cli (which depends on internal/check,
		// and so transitively reaches the same runtime/ packages) and for
		// cmd/nise (which depends on both): neither imports a
		// ./runtime/... package directly, so the CLI's ordinary call
		// graph does not force every ancestor of internal/check onto this
		// allowlist too — only the one package that actually wrote the
		// import statement does.
		if networkAllowlist[pkg.ImportPath] != "" {
			continue
		}
		reachedSet := make(map[string]bool)
		for _, imp := range pkg.Imports {
			if !strings.HasPrefix(imp, runtimePrefix) {
				continue
			}
			if runtimeNetworkSources[imp] {
				reachedSet[imp] = true
			}
			for _, d := range runtimeDeps[imp] {
				if strings.HasPrefix(d, runtimePrefix) && runtimeNetworkSources[d] {
					reachedSet[d] = true
				}
			}
		}
		if len(reachedSet) == 0 {
			continue
		}
		reached := make([]string, 0, len(reachedSet))
		for r := range reachedSet {
			reached = append(reached, r)
		}
		sort.Strings(reached)

		exception, ok := transitiveNetworkAllowlist[pkg.ImportPath]
		if !ok {
			t.Errorf("%s transitively reaches network-capable package(s) %s through ./runtime/..., "+
				"and is not in transitiveNetworkAllowlist (static_test.go)", pkg.ImportPath, strings.Join(reached, ", "))
			continue
		}
		allowed := make(map[string]bool, len(exception.runtimeDeps))
		for _, dep := range exception.runtimeDeps {
			allowed[dep] = true
		}
		var widened []string
		for _, dep := range reached {
			if !allowed[dep] {
				widened = append(widened, dep)
			}
		}
		if len(widened) > 0 {
			t.Errorf("%s now transitively reaches %s through ./runtime/..., beyond the named set in "+
				"transitiveNetworkAllowlist[%q].runtimeDeps (%s) — widen the exception deliberately, with a reason, or undo the new dependency",
				pkg.ImportPath, strings.Join(widened, ", "), pkg.ImportPath, strings.Join(exception.runtimeDeps, ", "))
		}
		if exception.reason == "" {
			t.Errorf("transitiveNetworkAllowlist[%q] has no reason given", pkg.ImportPath)
		}
		transitiveSeen[pkg.ImportPath] = true
	}

	for entry := range networkAllowlist {
		if !directSeen[entry] {
			t.Errorf("networkAllowlist names %s, which either does not exist under ./cmd/... or "+
				"./internal/... or no longer directly imports a network package — remove the stale entry", entry)
		}
	}
	for entry := range transitiveNetworkAllowlist {
		if !transitiveSeen[entry] {
			t.Errorf("transitiveNetworkAllowlist names %s, which either does not exist under ./cmd/... or "+
				"./internal/..., or no longer transitively reaches any of its named runtimeDeps — remove the stale entry", entry)
		}
	}
}

// importsAny returns the subset of forbidden present in imports, sorted.
func importsAny(imports, forbidden []string) []string {
	forbiddenSet := make(map[string]bool, len(forbidden))
	for _, f := range forbidden {
		forbiddenSet[f] = true
	}
	var hit []string
	for _, imp := range imports {
		if forbiddenSet[imp] {
			hit = append(hit, imp)
		}
	}
	sort.Strings(hit)
	return hit
}

// TestStaticRuntimeNeverImportsInternal enforces the assertion ADR 0011
// makes in prose but nothing previously checked: runtime/ and modules/ are
// the only packages an application may import (Global Constraint 9), and
// ADR 0011 states the dependency direction is one way — internal/ may
// import runtime/, never the reverse. That only holds if runtime/ itself
// never reaches back into internal/: an application importing runtime/foo
// must not transitively pull in a package that is documented as private
// and free to change in any release.
func TestStaticRuntimeNeverImportsInternal(t *testing.T) {
	t.Parallel()

	pkgs := listPackages(t, modulePath+"/runtime/...")

	internalPrefix := modulePath + "/internal/"
	for _, pkg := range pkgs {
		for _, imp := range pkg.Imports {
			if strings.HasPrefix(imp, internalPrefix) {
				t.Errorf("%s imports %s: no runtime/ package may import internal/ (ADR 0011)", pkg.ImportPath, imp)
			}
		}
	}
}
