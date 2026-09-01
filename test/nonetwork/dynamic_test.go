package nonetwork

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// niseBinary is the real nise binary, built once by TestMain and shared by
// every dynamic test in this package. Running the actual compiled binary
// as a subprocess — rather than calling internal/cli.Execute in-process —
// is what the task requires and what makes this proof honest: an
// in-process call would run inside this test binary's own process
// environment, and Go's net/http proxy configuration is read once and
// cached, so a later os.Setenv from the test would not reliably change
// what an in-process client actually does. A real subprocess gets a real,
// freshly-read environment every time, exactly like a user's shell
// invocation would.
var niseBinary string

func TestMain(m *testing.M) {
	cleanup, err := buildNiseBinary()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nonetwork: building nise binary for the dynamic proof:", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// buildNiseBinary compiles ./cmd/nise into a scratch directory once,
// setting the package-level niseBinary, so every dynamic test case in this
// package execs the same binary instead of each paying its own `go
// build`.
func buildNiseBinary() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "nise-nonetwork-")
	if err != nil {
		return func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	name := "nise"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(dir, name)

	// #nosec G204 -- fixed arguments, no user input.
	cmd := exec.Command("go", "build", "-o", out, modulePath+"/cmd/nise")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if buildErr := cmd.Run(); buildErr != nil {
		cleanup()
		return func() {}, fmt.Errorf("go build -o %s %s/cmd/nise: %w\n%s", out, modulePath, buildErr, stderr.String())
	}
	niseBinary = out
	return cleanup, nil
}

// connRecorder records the remote address of every connection a
// recordingProxy accepts. It exists so a test can assert "zero connection
// attempts" as a number, not infer it from the absence of a crash.
//
// It also records where each connection was *going*, which the remote address
// cannot say: through a proxy, every connection arrives from loopback. A
// proxied client names its destination in the first request line — CONNECT
// host:443 for TLS, an absolute-form GET for plain HTTP — so reading that one
// line before closing turns "something dialled out" into "something dialled
// out to this host". The zero-connection cases do not need it; the one command
// that is *supposed* to dial does, or it could be reaching anywhere.
type connRecorder struct {
	mu           sync.Mutex
	attempts     []string
	destinations []string
}

func (r *connRecorder) record(remote string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts = append(r.attempts, remote)
}

func (r *connRecorder) recordDestination(host string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.destinations = append(r.destinations, host)
}

func (r *connRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.attempts))
	copy(out, r.attempts)
	return out
}

func (r *connRecorder) destinationSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.destinations))
	copy(out, r.destinations)
	return out
}

// requestLineHost extracts the destination from a proxied request's first
// line. It returns "" when the line names none, which every caller treats as
// "the connection told us nothing" rather than as a host.
func requestLineHost(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	target := fields[1]
	// CONNECT api.github.com:443 HTTP/1.1
	if strings.EqualFold(fields[0], "CONNECT") {
		host, _, err := net.SplitHostPort(target)
		if err != nil {
			return target
		}
		return host
	}
	// GET http://host/path HTTP/1.1 — absolute-form, which is how a client
	// addresses a plain-HTTP proxy.
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Hostname()
}

// startRecordingProxy listens on loopback, records the remote address of
// every connection it accepts, and then closes it immediately — refusing
// whatever protocol the client tried to speak (a bare TCP close reads to
// an HTTP client as a broken connection, which is loud: the client gets an
// error, not a silent hang or a believable-looking response). It returns
// the "host:port" to point HTTP_PROXY/HTTPS_PROXY/ALL_PROXY at, the
// recorder, and a stop function that must be called after the subprocess
// under test has exited.
func startRecordingProxy(t *testing.T) (addr string, rec *connRecorder, stop func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("nonetwork: could not start the recording proxy listener: %v", err)
	}
	rec = &connRecorder{}
	acceptDone := make(chan struct{})
	var handling sync.WaitGroup
	go func() {
		defer close(acceptDone)
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			rec.record(conn.RemoteAddr().String())
			handling.Add(1)
			go func() {
				defer handling.Done()
				// One line, under a short deadline, then closed as before.
				// The deadline matters: a client that connects and sends
				// nothing must not hold this goroutine open past the test.
				_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				line, readErr := bufio.NewReader(conn).ReadString('\n')
				if readErr == nil {
					if host := requestLineHost(line); host != "" {
						rec.recordDestination(host)
					}
				}
				_ = conn.Close()
			}()
		}
	}()
	stop = func() {
		_ = ln.Close()
		<-acceptDone
		handling.Wait()
	}
	return ln.Addr().String(), rec, stop
}

// hermeticEnv builds the subprocess environment: the real process
// environment (so PATH, HOME, and TMPDIR still resolve, which doctor and
// os.MkdirTemp both need), with every existing *_PROXY variable and
// GOPROXY stripped and replaced by ones that point at proxyAddr and
// disable the module proxy outright.
//
// Honesty check, stated once here rather than at every call site: setting
// HTTP_PROXY/HTTPS_PROXY/ALL_PROXY only constrains Go code that builds an
// http.Client or http.Transport without overriding Proxy — which is what
// net/http.DefaultTransport does, via ProxyFromEnvironment, unless code
// explicitly opts out. It does nothing to a raw net.Dial or net.DialTCP
// call, which ignores proxy environment variables entirely and would try
// to reach its destination directly. See netns_test.go for the
// complementary, stricter check this gap motivates, and this package's
// doc comment for the summary.
func hermeticEnv(proxyAddr string) []string {
	proxyURL := "http://" + proxyAddr

	var out []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(key, "GOPROXY") || strings.HasSuffix(strings.ToUpper(key), "_PROXY") {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"HTTP_PROXY="+proxyURL,
		"HTTPS_PROXY="+proxyURL,
		"ALL_PROXY="+proxyURL,
		"NO_PROXY=",
		"http_proxy="+proxyURL,
		"https_proxy="+proxyURL,
		"all_proxy="+proxyURL,
		"no_proxy=",
		// GOPROXY=off is "where relevant": none of the commands this test
		// runs invoke the go toolchain themselves (nise test --help only
		// prints usage; it never calls `go test`), but setting it costs
		// nothing and closes the door if that ever changes.
		"GOPROXY=off",
	)
}

// runResult is one subprocess invocation's observable outcome.
type runResult struct {
	exitCode       int
	stdout, stderr string
}

// runNise execs the built nise binary with args, in dir, under env,
// bounded by a generous timeout so a hang (which a silently-blocked dial
// would cause) fails the test instead of the test suite.
func runNise(t *testing.T, dir string, env []string, args ...string) runResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// #nosec G204 -- niseBinary is a path this package built in TestMain;
	// args come from this package's own fixed dynamicCases table, never
	// external input.
	cmd := exec.CommandContext(ctx, niseBinary, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("nise %s timed out after 20s (stdout=%q stderr=%q) — a hang here is consistent with a blocked network dial",
			strings.Join(args, " "), stdout.String(), stderr.String())
	}

	res := runResult{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		res.exitCode = 0
	case errors.As(runErr, &exitErr):
		res.exitCode = exitErr.ExitCode()
	default:
		t.Fatalf("nise %s: could not run subprocess: %v (stderr=%q)", strings.Join(args, " "), runErr, stderr.String())
	}
	return res
}

// dynCase is one non-interactive invocation the dynamic proof covers.
type dynCase struct {
	name string
	args []string
	// wantExit, when non-nil, is asserted exactly. Left nil for "doctor",
	// whose exit code legitimately depends on which local tools this
	// machine happens to have installed — this test cares whether it
	// touched the network, not whether the sandbox has Node installed.
	wantExit *int
	// extraChecks runs after the exit-code assertion, for cases that need
	// to assert something about *why* the command failed (e.g. that
	// "check" outside a project failed locally, not over the network).
	extraChecks func(t *testing.T, res runResult)
}

func exitPtr(code int) *int { return &code }

// networkErrorMarkers are substrings that would appear in a Go error if a
// dial were actually attempted and failed (through the recording proxy,
// through DNS, or directly) — used by dynCase.extraChecks to assert a
// command's failure is the local one it claims to be, not a network one in
// disguise.
var networkErrorMarkers = []string{
	"dial tcp",
	"connection refused",
	"no such host",
	"network is unreachable",
	"proxyconnect",
	"i/o timeout",
	"context deadline exceeded",
}

func assertNoNetworkErrorMarkers(t *testing.T, res runResult) {
	t.Helper()
	combined := strings.ToLower(res.stdout + "\n" + res.stderr)
	for _, marker := range networkErrorMarkers {
		if strings.Contains(combined, marker) {
			t.Errorf("output mentions %q, which looks like a network error, not a local one:\nstdout=%q\nstderr=%q",
				marker, res.stdout, res.stderr)
		}
	}
}

// dynamicCases is the exact command list the dynamic proof covers. It
// satisfies the task brief's minimum ("no args, help, version, doctor,
// generate --help, test --help"), the controller's two additions ("check
// (outside a project), and new (into a temp directory)"), and review
// fix-round-1's "agents (outside a project)": agents.Commands() is the
// only entry in the registry this list omitted, and it lives in the same
// wholesale-allowlisted internal/cli package as dev.go — see
// networkAllowlist's entry for internal/cli — where this dynamic proof is
// the only layer able to catch anything at all, since the static import
// check cannot tell "agents.go" apart from "dev.go" within one compiled
// package.
//
// This list is hand-maintained against internal/cli.Commands() rather
// than derived from it. Deriving it would need test/nonetwork to import
// internal/cli to call Commands() directly — which would put a package
// this suite's own static_test.go treats as network-capable into the
// test binary's own import graph, muddying what TestStaticNoUnexpected-
// NetworkImports is checking (a test importing the thing it audits is a
// different, weaker shape of test than one that shells out to the
// compiled artifact, which is what makes the dynamic proof honest in the
// first place — see this file's own top-of-file comment on why a real
// subprocess is used at all). The alternative — parsing registry.go's
// source text to enumerate command names without an import — trades one
// hand-maintained list for a different fragile one. A short, commented,
// hand-maintained table, reviewed here, is the honest answer for a
// registry that grows by single-digit entries per milestone; whoever adds
// the next command to internal/cli/registry.go should add its
// non-interactive shape here in the same change.
//
// nise dev is deliberately not included: it cannot run non-interactively
// (it supervises a container database and a Vite dev server, and refuses
// to start outside a generated project with the frontend already
// installed), and it is exactly the command networkAllowlist in
// static_test.go says is expected to open sockets. Proving it opens
// *only* the loopback sockets its own topology promises is a larger,
// integration-style test, and out of scope for this task's static/dynamic
// split — see docs/adr/0012-dev-loop-proxy-topology.md for that contract.
func dynamicCases() []dynCase {
	return []dynCase{
		{name: "no args", args: nil, wantExit: exitPtr(0)},
		{name: "help", args: []string{"help"}, wantExit: exitPtr(0)},
		{name: "version", args: []string{"version"}, wantExit: exitPtr(0)},
		{name: "doctor", args: []string{"doctor"}},
		{name: "generate --help", args: []string{"generate", "--help"}, wantExit: exitPtr(0)},
		{name: "test --help", args: []string{"test", "--help"}, wantExit: exitPtr(0)},
		{
			name:     "check (outside a project)",
			args:     []string{"check"},
			wantExit: exitPtr(3), // clierr.ExitPrecondition
			extraChecks: func(t *testing.T, res runResult) {
				if !strings.Contains(res.stderr, "no nise.json") {
					t.Errorf("stderr = %q, want it to name the local precondition (\"no nise.json found\") — "+
						"this case exists specifically to prove the failure is local, not a network timeout", res.stderr)
				}
			},
		},
		{
			// agents lives in the wholesale-allowlisted internal/cli
			// package (see networkAllowlist's entry for it), where this
			// dynamic proof is the only layer that can catch anything —
			// the static import check cannot distinguish "agents.go"
			// from "dev.go" within one compiled package. Same shape as
			// "check": findProjectRoot fails the same way outside a
			// project, through clierr.Precondition.
			name:     "agents (outside a project)",
			args:     []string{"agents"},
			wantExit: exitPtr(3), // clierr.ExitPrecondition
			extraChecks: func(t *testing.T, res runResult) {
				if !strings.Contains(res.stderr, "no nise.json") {
					t.Errorf("stderr = %q, want it to name the local precondition (\"no nise.json found\") — "+
						"this case exists specifically to prove the failure is local, not a network timeout", res.stderr)
				}
			},
		},
		{
			name:     "new (into a temp directory)",
			args:     []string{"new", "demoapp", "--module-path", "example.com/demoapp", "--yes"},
			wantExit: exitPtr(0),
		},
		{
			// upgrade reads a project, rewrites Nise-owned files, and bumps
			// pinned dependency versions — all of which it does from data
			// compiled into the binary. `go mod tidy` is named in its output
			// rather than run, precisely so an upgrade is not the command
			// that discovers a dependency is unreachable; this case is what
			// proves that stayed true.
			name:     "upgrade (outside a project)",
			args:     []string{"upgrade", "--dry-run"},
			wantExit: exitPtr(3), // clierr.ExitPrecondition
			extraChecks: func(t *testing.T, res runResult) {
				if !strings.Contains(res.stderr, "no nise.json") {
					t.Errorf("stderr = %q, want it to name the local precondition (\"no nise.json found\") — "+
						"this case exists specifically to prove the failure is local, not a network timeout", res.stderr)
				}
			},
		},
	}
}

// TestDynamicNoOutboundConnections is the dynamic proof: every
// non-interactive command in dynamicCases runs as a real subprocess whose
// HTTP_PROXY/HTTPS_PROXY/ALL_PROXY point at a listener that records every
// connection it receives, and the test asserts that listener recorded
// nothing.
//
// This is the proof that matters more than the static one (see this
// package's doc comment): it catches what an import graph cannot, such as
// a subprocess nise itself spawns (doctor's `go version`, `node
// --version`, `docker --version`) reaching out on its own. It does not
// catch a raw net.Dial that ignores the proxy environment entirely —
// hermeticEnv's doc comment states that gap precisely, and
// TestDynamicNetworkNamespace (netns_test.go) is the stricter,
// best-effort check for exactly that gap.
func TestDynamicNoOutboundConnections(t *testing.T) {
	for _, c := range dynamicCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			addr, rec, stop := startRecordingProxy(t)
			defer stop()

			res := runNise(t, dir, hermeticEnv(addr), c.args...)

			if c.wantExit != nil && res.exitCode != *c.wantExit {
				t.Errorf("nise %s: exit code = %d, want %d\nstdout=%q\nstderr=%q",
					strings.Join(c.args, " "), res.exitCode, *c.wantExit, res.stdout, res.stderr)
			}
			if c.extraChecks != nil {
				c.extraChecks(t, res)
			}
			assertNoNetworkErrorMarkers(t, res)

			if attempts := rec.snapshot(); len(attempts) > 0 {
				t.Errorf("nise %s made %d outbound connection attempt(s) via HTTP(S)_PROXY, want 0: %s",
					strings.Join(c.args, " "), len(attempts), strings.Join(attempts, ", "))
			}
		})
	}
}

// TestDynamicNoOutboundConnectionsUnderLoad guards against a recorder that
// only happens to catch the *first* connection: it runs "doctor" — the
// command most likely to fan out into several subprocesses (one exec per
// tool it checks) — several times against one shared recorder to make sure
// repeated invocations don't each get one "free" connection some off-by-
// one in the harness might hide.
func TestDynamicNoOutboundConnectionsUnderLoad(t *testing.T) {
	t.Parallel()

	addr, rec, stop := startRecordingProxy(t)
	defer stop()

	const runs = 3
	for i := 0; i < runs; i++ {
		dir := t.TempDir()
		res := runNise(t, dir, hermeticEnv(addr), "doctor")
		assertNoNetworkErrorMarkers(t, res)
	}
	if attempts := rec.snapshot(); len(attempts) > 0 {
		t.Errorf("nise doctor made %d outbound connection attempt(s) across %d runs, want 0: %s",
			len(attempts), runs, strings.Join(attempts, ", "))
	}
}

// TestDynamicTheUpdateCheckIsTheOneNetworkCommand is the inverse of every
// other proof in this package, and it exists because a guarantee with one
// exception is only as good as the exception being *exactly* one.
//
// `nise version check` is the explicit update check (docs/cli-and-distribution.md).
// It is allowed to reach the network — a person typed a command whose entire
// purpose is to ask a question of a server. Two things have to be true about
// that, and neither is provable by reading source:
//
//  1. It actually does reach the network. An allowlist entry for a package
//     that no longer dials is a permission nobody reviewed still standing, and
//     an update check that silently stopped checking would pass every other
//     test in this file.
//  2. It reaches exactly one host, once. The dynamic proof's recording proxy
//     is the same one every other case uses, so "one connection to one host"
//     is measured on the same instrument that measures "none" everywhere else.
func TestDynamicTheUpdateCheckIsTheOneNetworkCommand(t *testing.T) {
	t.Parallel()

	addr, rec, stop := startRecordingProxy(t)
	defer stop()

	// The recording proxy refuses every connection it receives, so this
	// command necessarily fails. The failure is the point: what is being
	// measured is the attempt, not the answer.
	_ = runNise(t, t.TempDir(), hermeticEnv(addr), "version", "check")

	attempts := rec.snapshot()
	if len(attempts) == 0 {
		t.Fatal("nise version check made no outbound connection attempt.\n" +
			"Either the explicit update check no longer reaches the network — in which case " +
			"networkAllowlist's entry for internal/release, and httpsHostAllowlist's entry for " +
			"api.github.com, are permissions nobody reviewed still standing — or this proof's " +
			"proxy trap no longer sees what it used to, in which case every other case in this " +
			"file is passing for the wrong reason.")
	}

	destinations := rec.destinationSnapshot()
	if len(destinations) == 0 {
		t.Fatalf("nise version check made %d connection attempt(s) and named no destination in any of them; "+
			"this proof cannot tell where the update check was going", len(attempts))
	}
	hosts := map[string]bool{}
	for _, host := range destinations {
		hosts[host] = true
	}
	if len(hosts) != 1 {
		t.Errorf("nise version check contacted %d hosts (%v); the update check talks to one host or fails", len(hosts), hosts)
	}
	for host := range hosts {
		if host != updateCheckHost {
			t.Errorf("nise version check contacted %q, and the documented release index is %q", host, updateCheckHost)
		}
	}
}

// updateCheckHost is the one host any nise command may contact. It is written
// out here rather than read from internal/release, so that a change to the
// endpoint has to pass through a second place that says what was intended.
const updateCheckHost = "api.github.com"
