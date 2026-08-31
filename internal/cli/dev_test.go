package cli

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/dev"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// devProjectDir writes a minimal project tree: a valid recipe and one
// command directory. Options tweak it for the failure cases.
func devProjectDir(t *testing.T, commands ...string) string {
	t.Helper()
	root := t.TempDir()
	r := recipe.Recipe{
		SchemaVersion:  recipe.SchemaVersion,
		Profile:        recipe.ProfileGoChiPostgresSvelte,
		CLIVersion:     "0.1.0",
		RuntimeVersion: "0.1.0",
	}
	if err := recipe.Save(filepath.Join(root, recipe.FileName), r); err != nil {
		t.Fatalf("writing the recipe: %v", err)
	}
	for _, c := range commands {
		if err := os.MkdirAll(filepath.Join(root, "cmd", c), 0o750); err != nil {
			t.Fatalf("creating cmd/%s: %v", c, err)
		}
	}
	return root
}

func TestDevIsRegistered(t *testing.T) {
	t.Parallel()
	for _, c := range Commands() {
		if c.Name == "dev" {
			if c.Run == nil || c.NewFlagSet == nil {
				t.Fatal(`the "dev" command is registered without a Run or a flag set`)
			}
			return
		}
	}
	t.Fatal(`"dev" is not in the command registry`)
}

// TestRefuseProductionEnvironment is the fail-closed check: `nise dev`
// starts an unauthenticated proxy, publishes a database with a derived
// password, and restarts a binary on every save. None of that may run
// against anything a person would call production, and the check must
// refuse every value it does not positively recognize rather than only the
// literal string "production".
func TestRefuseProductionEnvironment(t *testing.T) {
	allowed := []string{"development", "test"}
	refused := []string{
		"production", "Production", "PRODUCTION", "prod", "prd",
		"staging", "live", " development", "development ", "dev",
	}

	for _, value := range allowed {
		t.Run("allows "+value, func(t *testing.T) {
			t.Setenv("APP_ENV", value)
			if err := refuseProductionEnvironment(); err != nil {
				t.Fatalf("APP_ENV=%q was refused: %v", value, err)
			}
		})
	}
	for _, value := range refused {
		t.Run("refuses "+value, func(t *testing.T) {
			t.Setenv("APP_ENV", value)
			err := refuseProductionEnvironment()
			if err == nil {
				t.Fatalf("APP_ENV=%q was accepted; nise dev must only run in development", value)
			}
			var ce *clierr.Error
			if !errors.As(err, &ce) {
				t.Fatalf("error %v is not a *clierr.Error", err)
			}
			if ce.ExitCode() != clierr.ExitPrecondition {
				t.Fatalf("exit code = %d, want %d", ce.ExitCode(), clierr.ExitPrecondition)
			}
			if ce.Code() != "dev.production_environment" {
				t.Fatalf("machine code = %q, want dev.production_environment", ce.Code())
			}
		})
	}
	t.Run("allows an unset APP_ENV", func(t *testing.T) {
		t.Setenv("APP_ENV", "")
		if err := os.Unsetenv("APP_ENV"); err != nil {
			t.Fatalf("unsetting APP_ENV: %v", err)
		}
		if err := refuseProductionEnvironment(); err != nil {
			t.Fatalf("an unset APP_ENV was refused: %v", err)
		}
	})
}

func TestSingleCommandName(t *testing.T) {
	t.Parallel()
	t.Run("one command", func(t *testing.T) {
		t.Parallel()
		got, err := singleCommandName(devProjectDir(t, "demoapp"))
		if err != nil || got != "demoapp" {
			t.Fatalf("singleCommandName = (%q, %v), want (demoapp, nil)", got, err)
		}
	})
	t.Run("no cmd directory", func(t *testing.T) {
		t.Parallel()
		_, err := singleCommandName(devProjectDir(t))
		assertDevError(t, err, clierr.ExitPrecondition, "dev.no_command")
	})
	t.Run("two commands", func(t *testing.T) {
		t.Parallel()
		_, err := singleCommandName(devProjectDir(t, "one", "two"))
		assertDevError(t, err, clierr.ExitPrecondition, "dev.ambiguous_command")
		if !strings.Contains(err.Error(), "one") || !strings.Contains(err.Error(), "two") {
			t.Fatalf("error %v does not name the candidates", err)
		}
	})
}

func TestRequireFrontendDependencies(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	err := requireFrontendDependencies(root)
	assertDevError(t, err, clierr.ExitPrecondition, "dev.frontend_not_installed")
	var ce *clierr.Error
	if !errors.As(err, &ce) || !strings.Contains(ce.Recovery(), "pnpm --dir frontend install") {
		t.Fatalf("recovery %q does not name the command that fixes it", ce.Recovery())
	}

	if err := os.MkdirAll(filepath.Join(root, "frontend", "node_modules"), 0o750); err != nil {
		t.Fatalf("creating node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "package.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("writing package.json: %v", err)
	}
	if err := requireFrontendDependencies(root); err != nil {
		t.Fatalf("requireFrontendDependencies = %v on an installed frontend, want nil", err)
	}
}

func TestValidateDevPortsRejectsBadValues(t *testing.T) {
	t.Parallel()
	base := devFlags{
		port: "8080", appPort: "8081", vitePort: "5173", dbPort: "5432",
		pollMilli: 300, graceMs: 6000, noDB: true,
	}
	tests := []struct {
		name   string
		mutate func(*devFlags)
		want   clierr.ExitCode
	}{
		{"bad proxy port", func(f *devFlags) { f.port = "0" }, clierr.ExitUsage},
		{"bad app port", func(f *devFlags) { f.appPort = "nope" }, clierr.ExitUsage},
		{"bad vite port", func(f *devFlags) { f.vitePort = "70000" }, clierr.ExitUsage},
		{"poll too small", func(f *devFlags) { f.pollMilli = 1 }, clierr.ExitUsage},
		{"poll too large", func(f *devFlags) { f.pollMilli = 999999 }, clierr.ExitUsage},
		{"grace too small", func(f *devFlags) { f.graceMs = 1 }, clierr.ExitUsage},
		{"grace too large", func(f *devFlags) { f.graceMs = 999999 }, clierr.ExitUsage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := base
			tc.mutate(&f)
			err := validateDevPorts(f)
			if err == nil {
				t.Fatal("validateDevPorts accepted an unusable value")
			}
			var ce *clierr.Error
			if !errors.As(err, &ce) || ce.ExitCode() != tc.want {
				t.Fatalf("error %v, want a *clierr.Error with exit %d", err, tc.want)
			}
		})
	}
}

// TestValidateDevPortsNeverProbesTheDatabasePort covers a regression: the
// normal path reuses a container that is already listening on the database
// port, so probing it for occupancy rejects the common case.
func TestValidateDevPortsNeverProbesTheDatabasePort(t *testing.T) {
	t.Parallel()
	held := listenOnAPort(t)

	// Choosing a free port and then asserting it is still free is racy by
	// construction: between the two, any other parallel test in this binary
	// — several of which bind a loopback port of their own — can be handed
	// the port the kernel just released. There is no way to close that
	// window, because holding the port would make it occupied, which is the
	// opposite of what this test needs.
	//
	// So it is retried rather than pretended away. What is under test is a
	// rule about which ports are probed at all, not port allocation; a
	// refusal that names the app port is the race, and a refusal that names
	// the database port is the regression. Three attempts make a spurious
	// failure vanishingly unlikely without ever hiding the real one.
	var err error
	for attempt := range 3 {
		app, vite, proxy := freePorts(t, 3)
		f := devFlags{
			port: proxy, appPort: app, vitePort: vite, dbPort: held,
			pollMilli: 300, graceMs: 6000, embedded: false,
		}
		if err = validateDevPorts(f); err == nil {
			return
		}
		if strings.Contains(err.Error(), held) {
			t.Fatalf("validateDevPorts = %v; a database port held by this project's own container must not be a refusal", err)
		}
		t.Logf("attempt %d: a chosen port was taken between allocation and validation: %v", attempt+1, err)
	}
	t.Fatalf("validateDevPorts never saw three free ports across three attempts; last error: %v", err)
}

// TestFrontendPreconditionIsCheckedBeforeTheDatabase pins the ordering the
// source reads in: a stat that can fail must run before a step that can pull
// a 250 MB image. A reader who reorders these would otherwise make a
// developer with no frontend/node_modules wait out a download before being
// told something that was already true when they pressed enter.
func TestFrontendPreconditionIsCheckedBeforeTheDatabase(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("dev.go")
	if err != nil {
		t.Fatalf("reading dev.go: %v", err)
	}
	body := string(src)
	frontend := strings.Index(body, "requireFrontendDependencies(root)")
	database := strings.Index(body, "startDevDatabase(")
	if frontend < 0 || database < 0 {
		t.Fatal("could not find both call sites in dev.go")
	}
	if frontend > database {
		t.Fatal("requireFrontendDependencies runs after startDevDatabase; the cheap precondition must come first")
	}
}

// TestDatabaseTeardownRunsOnEveryExitPath covers the leak the review found:
// any failure after the database is up — a failed proxy build, a failed
// frontend build, a proxy port taken between the pre-flight check and the
// listen — returned without honoring --stop-db and without printing the stop
// hint, so a container was left running and a flag silently did nothing.
//
// The guarantee is structural rather than incidental, so it is asserted
// structurally: the teardown is deferred immediately after the database
// starts, and the ordered shutdown calls the same once-guarded closure so the
// happy path keeps its line ordering without running the teardown twice.
func TestDatabaseTeardownRunsOnEveryExitPath(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("dev.go")
	if err != nil {
		t.Fatalf("reading dev.go: %v", err)
	}
	body := string(src)

	start := strings.Index(body, "startDevDatabase(")
	deferred := strings.Index(body, "defer teardownDatabase()")
	if start < 0 || deferred < 0 {
		t.Fatal("could not find the database start and its deferred teardown in dev.go")
	}
	if deferred < start {
		t.Fatal("the teardown is deferred before the database is started")
	}
	// Nothing may return between the two: every path after the container is
	// up has to reach the defer.
	between := body[start:deferred]
	if strings.Count(between, "return err") > 1 {
		t.Fatalf("more than one early return sits between starting the database and deferring its teardown:\n%s", between)
	}
	// The ordered shutdown must go through the same closure, not a second
	// direct call that would double-print or double-stop.
	if strings.Count(body, "stopDevDatabase(") != 1 {
		t.Fatal("stopDevDatabase is called from more than one place; route every path through teardownDatabase")
	}
	if !strings.Contains(body, "teardownOnce.Do(") {
		t.Fatal("the teardown is not once-guarded; the deferred call would repeat the ordered shutdown's")
	}
}

func TestAppListenPortFollowsEmbeddedMode(t *testing.T) {
	t.Parallel()
	f := devFlags{port: "8080", appPort: "8081"}
	if got := appListenPort(f); got != "8081" {
		t.Fatalf("appListenPort = %q, want the app port when a proxy is in front", got)
	}
	f.embedded = true
	if got := appListenPort(f); got != "8080" {
		t.Fatalf("appListenPort = %q in --embedded mode, want the port the developer opens", got)
	}
}

// TestChildEnvOverlaysWithoutWeakeningAProductionDefault is the security
// property this command is held to: development may not turn on anything
// production leaves off.
func TestChildEnvOverlaysWithoutWeakeningAProductionDefault(t *testing.T) {
	t.Setenv("PORT", "9999")
	t.Setenv("TRUST_PROXY_HEADERS", "false")
	t.Setenv("DEBUG", "false")
	if err := os.Unsetenv("APP_ENV"); err != nil {
		t.Fatalf("unsetting APP_ENV: %v", err)
	}

	pg := dev.NewPostgres("demoapp", "", "127.0.0.1", "5432")
	env := childEnv(devFlags{port: "8080", appPort: "8081"}, &pg, true)
	got := envMap(env)

	if got["PORT"] != "8081" {
		t.Fatalf("PORT = %q, want the app port to win over the inherited value", got["PORT"])
	}
	if got["BIND_ADDR"] != "127.0.0.1" {
		t.Fatalf("BIND_ADDR = %q, want loopback", got["BIND_ADDR"])
	}
	if got["APP_ENV"] != "development" {
		t.Fatalf("APP_ENV = %q, want development when the developer set none", got["APP_ENV"])
	}
	if got["DATABASE_URL"] != pg.DSN() {
		t.Fatalf("DATABASE_URL = %q, want the real connection string", got["DATABASE_URL"])
	}
	// The two production defaults nise dev must not touch.
	if got["TRUST_PROXY_HEADERS"] != "false" {
		t.Fatalf("TRUST_PROXY_HEADERS = %q; nise dev must not enable it", got["TRUST_PROXY_HEADERS"])
	}
	if got["DEBUG"] != "false" {
		t.Fatalf("DEBUG = %q; nise dev must not choose it for the application", got["DEBUG"])
	}
	// Exactly one entry per name: a duplicate would make which value wins
	// depend on the child's own environment parsing.
	seen := map[string]int{}
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		seen[name]++
	}
	for name, n := range seen {
		if n > 1 {
			t.Fatalf("%s appears %d times in the child environment", name, n)
		}
	}
}

func TestChildEnvKeepsAnExplicitAppEnv(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	got := envMap(childEnv(devFlags{appPort: "8081"}, nil, true))
	if got["APP_ENV"] != "test" {
		t.Fatalf("APP_ENV = %q, want the developer's own value preserved", got["APP_ENV"])
	}
	if _, ok := got["DATABASE_URL"]; ok {
		t.Fatal("DATABASE_URL was set with no database running")
	}
}

func TestChildEnvDisablesChildColorWhenColorIsOff(t *testing.T) {
	// Isolate this assertion from the process running the test. An inherited
	// NO_COLOR (as used by many CI and agent environments) is the developer's
	// value to preserve, not one childEnv forced itself.
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")

	got := envMap(childEnv(devFlags{appPort: "8081"}, nil, false))
	if got["NO_COLOR"] != "1" || got["FORCE_COLOR"] != "0" {
		t.Fatalf("NO_COLOR=%q FORCE_COLOR=%q, want the children asked not to color",
			got["NO_COLOR"], got["FORCE_COLOR"])
	}
	got = envMap(childEnv(devFlags{appPort: "8081"}, nil, true))
	if got["NO_COLOR"] != "" || got["FORCE_COLOR"] != "" {
		t.Fatalf("NO_COLOR=%q FORCE_COLOR=%q, want the inherited values preserved on a color-capable terminal",
			got["NO_COLOR"], got["FORCE_COLOR"])
	}
}

// TestDevTopologyNeverCarriesAPassword is the one assertion that matters
// about this command's output: the startup block and the JSON document are
// both built from the redacted connection string.
func TestDevTopologyNeverCarriesAPassword(t *testing.T) {
	t.Parallel()
	pg := dev.NewPostgres("demoapp", "", "127.0.0.1", "5432")
	topology := devTopology{
		Project:      "demoapp",
		ProxyURL:     "http://127.0.0.1:8080",
		AppURL:       "http://127.0.0.1:8081",
		WebURL:       "http://127.0.0.1:5173",
		AppPrefixes:  dev.NewRouter(nil).Prefixes(),
		DatabaseURL:  pg.RedactedDSN(),
		DatabaseName: pg.ContainerName(),
		Runtime:      "podman 5.8.4",
	}

	encoded, err := json.Marshal(topology)
	if err != nil {
		t.Fatalf("marshaling the topology: %v", err)
	}
	for name, text := range map[string]string{"Human()": topology.Human(), "JSON": string(encoded)} {
		if strings.Contains(text, pg.DSN()) {
			t.Fatalf("%s carries the real connection string", name)
		}
		if !strings.Contains(text, "[REDACTED]") {
			t.Fatalf("%s does not carry the redaction placeholder:\n%s", name, text)
		}
	}

	human := topology.Human()
	for _, want := range []string{"http://127.0.0.1:8080", "http://127.0.0.1:8081", "http://127.0.0.1:5173", "/api", "demoapp"} {
		if !strings.Contains(human, want) {
			t.Fatalf("the startup block does not mention %q:\n%s", want, human)
		}
	}
}

func TestDevTopologyHumanDropsTheViteRowInEmbeddedMode(t *testing.T) {
	t.Parallel()
	human := devTopology{
		Project: "demoapp", ProxyURL: "http://127.0.0.1:8080",
		AppURL: "http://127.0.0.1:8080", Embedded: true,
	}.Human()
	if strings.Contains(human, "Vite") {
		t.Fatalf("--embedded mode still advertises Vite:\n%s", human)
	}
	if !strings.Contains(human, "embedded frontend build") {
		t.Fatalf("--embedded mode does not say what it is serving:\n%s", human)
	}
}

// assertDevError checks an error's exit code and machine-readable code.
func assertDevError(t *testing.T, err error, want clierr.ExitCode, code string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var ce *clierr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *clierr.Error", err)
	}
	if ce.ExitCode() != want {
		t.Fatalf("exit code = %d, want %d", ce.ExitCode(), want)
	}
	if ce.Code() != code {
		t.Fatalf("machine code = %q, want %q", ce.Code(), code)
	}
}

// envMap turns an environment slice into a map for assertions.
func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		out[name] = value
	}
	return out
}

// listenOnAPort binds a loopback port and holds it for the test's lifetime,
// returning the port as a string.
func listenOnAPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("splitting the address: %v", err)
	}
	return port
}

// freePorts returns n distinct loopback ports that were just released.
//
// All n listeners are opened before any is closed. Binding and releasing one
// at a time would let the kernel hand the same ephemeral port back on the
// next call, so three "free ports" could be one port three times — a project
// whose proxy, app, and Vite ports collided, tested as if they did not.
func freePorts(t *testing.T, n int) (string, string, string) {
	t.Helper()
	if n != 3 {
		t.Fatalf("freePorts: this helper returns exactly three ports, asked for %d", n)
	}

	listeners := make([]net.Listener, 0, n)
	ports := make([]string, 0, n)
	for range n {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listening: %v", err)
		}
		listeners = append(listeners, ln)
		_, port, err := net.SplitHostPort(ln.Addr().String())
		if err != nil {
			t.Fatalf("splitting the address: %v", err)
		}
		ports = append(ports, port)
	}
	for _, ln := range listeners {
		if err := ln.Close(); err != nil {
			t.Fatalf("closing: %v", err)
		}
	}
	return ports[0], ports[1], ports[2]
}
