package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/cli/output"
	"github.com/drilonrecica/nise-and-go/internal/dev"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// The source labels that prefix each child's interleaved output. They are
// short and fixed-width-ish so a terminal's left edge stays readable.
const (
	sourceDev = "dev"
	sourceApp = "app"
	sourceWeb = "web"
	sourceDB  = "db"
)

// devPrinter is how everything in `nise dev` prints. It exists so no code
// path below reaches for os.Stdout: every line, whether nise's own or a
// child's, goes through the one output.Writer the dispatcher built for the
// resolved mode, which is what keeps a --json invocation's stdout free of
// interleaved child text and a --quiet one silent.
type devPrinter struct{ out output.Writer }

// raw prints a line a PrefixWriter has already tagged.
func (p devPrinter) raw(line string) { p.out.Line(line) }

// say prints one of nise's own lines, tagged with its source.
func (p devPrinter) say(source, msg string) { p.out.Line("[" + source + "] " + msg) }

// devDocs is the documentation reference every error from this command
// carries.
const devDocs = "docs/commands/dev.md"

// proxyReadHeaderTimeout bounds how long a client may take to send its
// request headers. There is deliberately no ReadTimeout or WriteTimeout:
// the proxy carries Vite's hot-module-reload websocket, which is a
// long-lived connection that either timeout would sever every few seconds.
const proxyReadHeaderTimeout = 20 * time.Second

// defaultDevStopGrace is how long a child gets to exit after SIGTERM
// before nise kills it.
//
// It is deliberately longer than the five-second drain the generated
// application performs on shutdown (runtime/lifecycle.DefaultDrainPeriod,
// which the generated internal/app/app.go does not make configurable). The
// consequence is that every rebuild costs that drain: the application
// really does run its production shutdown path on every save, which is the
// same "development must not diverge from production" argument this
// command's whole topology rests on — a dev loop that SIGKILLs the server
// on every save is a dev loop where nobody ever exercises the shutdown
// code. --stop-grace shortens it for anyone who would rather have the
// second back.
const defaultDevStopGrace = 6 * time.Second

// databaseReadyTimeout bounds the health gate. A first run has to
// initialize a cluster on a cold volume, which is the slow case.
const databaseReadyTimeout = 90 * time.Second

// devTopology is `nise dev`'s output.Result: the addresses and the
// container it brought up. It is printed once at startup, and it is the
// single JSON document a `nise dev --json` invocation emits, so a script
// can learn the ports without scraping prose.
//
// Every field is safe to print. DatabaseURL is the redacted form; the real
// connection string exists only in the application child's environment.
type devTopology struct {
	Project      string   `json:"project"`
	ProxyURL     string   `json:"proxyUrl"`
	AppURL       string   `json:"appUrl"`
	WebURL       string   `json:"webUrl,omitempty"`
	AppPrefixes  []string `json:"appPrefixes,omitempty"`
	DatabaseURL  string   `json:"databaseUrl,omitempty"`
	DatabaseName string   `json:"databaseContainer,omitempty"`
	Runtime      string   `json:"containerRuntime,omitempty"`
	Embedded     bool     `json:"embedded"`
}

// Human renders the startup block.
func (t devTopology) Human() string {
	var b strings.Builder
	fmt.Fprintf(&b, "nise dev — %s\n\n", t.Project)
	fmt.Fprintf(&b, "  open      %s\n", t.ProxyURL)
	if t.Embedded {
		fmt.Fprintf(&b, "  app       %s  (serving the embedded frontend build, production headers)\n", t.AppURL)
	} else {
		fmt.Fprintf(&b, "  app       %s  %s\n", t.AppURL, strings.Join(t.AppPrefixes, ", "))
		fmt.Fprintf(&b, "  web       %s  everything else (Vite)\n", t.WebURL)
	}
	if t.DatabaseURL != "" {
		fmt.Fprintf(&b, "  database  %s\n", t.DatabaseURL)
		fmt.Fprintf(&b, "            container %s on %s\n", t.DatabaseName, t.Runtime)
	}
	return b.String()
}

// devFlags holds every flag `nise dev` defines, so the constructor's
// closure and Run share one value rather than a dozen loose variables.
type devFlags struct {
	port      string
	appPort   string
	vitePort  string
	dbPort    string
	dbImage   string
	runtime   string
	noDB      bool
	stopDB    bool
	embedded  bool
	pollMilli int
	graceMs   int
}

// devCommand implements `nise dev` (Nise task M1-011): the development
// loop. It supervises a container-run PostgreSQL, the application's Go
// server with rebuild-on-change, and Vite, and it fronts all of them with
// one reverse proxy so the browser sees exactly one origin.
//
// The topology, the alternative that was rejected, and the constraint the
// choice imposes on the later cookie-prefix decision are in
// docs/adr/0012-dev-loop-proxy-topology.md.
func devCommand() *Command {
	var f devFlags
	return &Command{
		Name:  "dev",
		Short: "Run the development loop: database, Go server, and Vite behind one origin",
		NewFlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("dev", flag.ContinueOnError)
			fs.StringVar(&f.port, "port", dev.DefaultProxyPort, "port the browser connects to")
			fs.StringVar(&f.appPort, "app-port", dev.DefaultAppPort, "port the Go server listens on, behind the proxy")
			fs.StringVar(&f.vitePort, "vite-port", dev.DefaultVitePort, "port the Vite dev server listens on, behind the proxy")
			fs.StringVar(&f.dbPort, "db-port", dev.DefaultDatabasePort, "loopback port the PostgreSQL container is published on")
			fs.StringVar(&f.dbImage, "db-image", dev.DefaultPostgresImage, "PostgreSQL image, pinned to an exact tag")
			fs.StringVar(&f.runtime, "container-runtime", "auto", `container runtime: "auto", "docker", or "podman"`)
			fs.BoolVar(&f.noDB, "no-db", false, "do not start a PostgreSQL container")
			fs.BoolVar(&f.stopDB, "stop-db", false, "stop the PostgreSQL container on exit instead of leaving it running")
			fs.BoolVar(&f.embedded, "embedded", false, "serve the embedded frontend build from the Go server instead of Vite (no hot reload; exercises the production Content Security Policy)")
			fs.IntVar(&f.pollMilli, "poll", int(dev.DefaultPollInterval/time.Millisecond), "source-watch poll interval in milliseconds")
			fs.IntVar(&f.graceMs, "stop-grace", int(defaultDevStopGrace/time.Millisecond), "milliseconds a child gets to shut down gracefully before it is killed")
			return fs
		},
		Run: func(ctx context.Context, env *Env) error { return runDev(ctx, env, f) },
	}
}

// runDev is `nise dev`'s body, split out of the constructor so the flag
// closure stays small.
func runDev(ctx context.Context, env *Env, f devFlags) error {
	root, project, err := resolveDevProject()
	if err != nil {
		return err
	}
	if err := refuseProductionEnvironment(); err != nil {
		return err
	}
	if err := validateDevPorts(f); err != nil {
		return err
	}

	// Color is decided from the detected terminal capabilities. Child
	// output is stripped of ANSI when color is off, and the children are
	// additionally told not to produce any.
	color := env.Caps.ColorEnabled()
	pr := devPrinter{out: env.Out}

	scratch, err := os.MkdirTemp("", "nise-dev-")
	if err != nil {
		return clierr.Wrap(err, clierr.ExitError,
			"could not create a scratch directory for the development build",
			"Check that the system temporary directory is writable.").WithDocs(devDocs)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	sigCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	runCtx, cancel := context.WithCancel(sigCtx)
	defer cancel()

	// The database comes up first and is health-gated, so nothing that
	// needs it is started against a cluster that is still initializing.
	db, dbRuntime, err := startDevDatabase(runCtx, env, f, project, pr)
	if err != nil {
		return err
	}

	topology := devTopology{
		Project:  project.name,
		ProxyURL: "http://127.0.0.1:" + f.port,
		AppURL:   "http://127.0.0.1:" + appListenPort(f),
		Embedded: f.embedded,
	}
	if db != nil {
		topology.DatabaseURL = db.RedactedDSN()
		topology.DatabaseName = db.ContainerName()
		topology.Runtime = dbRuntime.String()
	}

	var proxySrv *http.Server
	if !f.embedded {
		if err := requireFrontendDependencies(root); err != nil {
			return err
		}
		proxy, err := buildDevProxy(f, pr)
		if err != nil {
			return err
		}
		topology.WebURL = "http://127.0.0.1:" + f.vitePort
		topology.AppPrefixes = proxy.Router().Prefixes()
		proxySrv = &http.Server{Handler: proxy, ReadHeaderTimeout: proxyReadHeaderTimeout}
	} else {
		if err := buildFrontendOnce(runCtx, root, color, pr); err != nil {
			return err
		}
	}

	env.Out.Result(topology)

	changes := make(chan struct{}, 1)
	watcher := &dev.Watcher{
		Root:     root,
		Interval: time.Duration(f.pollMilli) * time.Millisecond,
		OnError:  func(err error) { pr.say(sourceDev, "watch: "+err.Error()) },
	}

	var (
		wg      sync.WaitGroup
		failMu  sync.Mutex
		failure error
	)
	fail := func(err error) {
		failMu.Lock()
		if failure == nil {
			failure = err
		}
		failMu.Unlock()
		cancel()
	}

	if proxySrv != nil {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", f.port))
		if err != nil {
			return portError("proxy", "--port", f.port, err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if serveErr := proxySrv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				fail(clierr.Wrap(serveErr, clierr.ExitError,
					"the development proxy stopped serving",
					"Re-run \"nise dev\".").WithDocs(devDocs))
			}
		}()

		web := viteSupervisor(root, f, color, pr)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if runErr := web.Run(runCtx, nil); runErr != nil {
				fail(clierr.Wrap(runErr, clierr.ExitPrecondition,
					"could not run the Vite development server",
					`Check that pnpm is installed and that "pnpm --dir frontend install" has been run.`).
					WithCode("dev.vite_failed").WithDocs(devDocs))
			}
		}()
	}

	app := appSupervisor(root, scratch, project, f, db, color, pr)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if runErr := app.Run(runCtx, changes); runErr != nil {
			fail(clierr.Wrap(runErr, clierr.ExitError,
				"could not run the application server",
				"See the [app] lines above for the underlying failure.").
				WithCode("dev.app_failed").WithDocs(devDocs))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		watcher.Run(runCtx, changes)
	}()

	env.Out.Line("")
	pr.say(sourceDev, fmt.Sprintf("watching %s for changes (poll %dms, debounce %dms) — press Ctrl-C to stop",
		strings.Join(dev.DefaultWatchDirs, ", "), f.pollMilli, dev.DefaultDebounce/time.Millisecond))

	<-runCtx.Done()
	pr.say(sourceDev, "shutting down")

	// The proxy stops accepting first, so a browser gets a refused
	// connection rather than a 502 from a child that is already dying.
	if proxySrv != nil {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		if err := proxySrv.Shutdown(shutdownCtx); err != nil {
			_ = proxySrv.Close()
		}
		cancelShutdown()
	}
	wg.Wait()
	stopDevDatabase(ctx, env, f, db, dbRuntime, pr)

	failMu.Lock()
	defer failMu.Unlock()
	if failure != nil {
		return failure
	}
	pr.say(sourceDev, "stopped")
	return nil
}

// devProject is the identity `nise dev` derives from the project on disk.
type devProject struct {
	// name is the application name, which is also the cmd/ subdirectory
	// and the binary's name.
	name string
}

// resolveDevProject locates the project root and the single command the
// application builds.
func resolveDevProject() (root string, project devProject, err error) {
	wd, wdErr := os.Getwd()
	if wdErr != nil {
		return "", devProject{}, clierr.Wrap(wdErr, clierr.ExitError,
			"could not determine the current directory",
			"Retry from a directory you have permission to read.")
	}
	root, found, findErr := findProjectRoot(wd)
	if findErr != nil {
		return "", devProject{}, clierr.Wrap(findErr, clierr.ExitError,
			fmt.Sprintf("could not search for %s", recipe.FileName),
			"Check filesystem permissions for the current directory and its parents.").WithDocs(devDocs)
	}
	if !found {
		return "", devProject{}, clierr.Precondition(
			fmt.Sprintf("no %s found in the current directory or any parent", recipe.FileName),
			`Run "nise new" to create a project, or run "nise dev" from inside one.`).
			WithCode("dev.no_project").WithDocs(devDocs)
	}
	if _, loadErr := recipe.Load(os.DirFS(root), recipe.FileName); loadErr != nil {
		return "", devProject{}, clierr.Wrap(loadErr, clierr.ExitPrecondition,
			fmt.Sprintf("%s does not parse", filepath.Join(root, recipe.FileName)),
			"Fix nise.json by hand, or restore it from version control; see docs/adr/0010-project-recipe-format.md.").
			WithCode("dev.invalid_recipe").WithDocs(devDocs)
	}
	name, nameErr := singleCommandName(root)
	if nameErr != nil {
		return "", devProject{}, nameErr
	}
	return root, devProject{name: name}, nil
}

// singleCommandName reports the one subdirectory of cmd/ that names the
// application's binary. The generated layout has exactly one; anything else
// is reported rather than guessed at, because building the wrong one would
// serve a different application on the port the developer opened.
func singleCommandName(root string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		return "", clierr.Wrap(err, clierr.ExitPrecondition,
			"could not read cmd/ in the project root",
			"A Nise project keeps its binary under cmd/<app>/; see docs/generated-application-layout.md.").
			WithCode("dev.no_command").WithDocs(devDocs)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	switch len(names) {
	case 1:
		return names[0], nil
	case 0:
		return "", clierr.Precondition(
			"cmd/ contains no command directory",
			"A Nise project keeps its binary under cmd/<app>/; see docs/generated-application-layout.md.").
			WithCode("dev.no_command").WithDocs(devDocs)
	default:
		return "", clierr.Precondition(
			fmt.Sprintf("cmd/ contains %d command directories (%s)", len(names), strings.Join(names, ", ")),
			"nise dev builds one binary. Remove the extra command directories, or run the one you want by hand.").
			WithCode("dev.ambiguous_command").WithDocs(devDocs)
	}
}

// refuseProductionEnvironment stops `nise dev` before it does anything when
// the resolved environment is not a development one.
//
// This is a fail-closed check, not a convenience: `nise dev` runs an
// unauthenticated reverse proxy, publishes a database with a derived
// password, and rebuilds and restarts a binary on every file change. None
// of that may happen against anything a person would call production, and
// the check refuses every value it does not positively recognize as
// development rather than only the literal string "production" — a typo
// like "Production" or a stray "prod" must not be read as permission.
func refuseProductionEnvironment() error {
	raw, set := os.LookupEnv("APP_ENV")
	if !set || raw == "" {
		return nil // nise dev sets APP_ENV=development for the child
	}
	switch raw {
	case "development", "test":
		return nil
	}
	return clierr.Precondition(
		fmt.Sprintf("APP_ENV is %q; nise dev only runs in a development environment", raw),
		`Unset APP_ENV, or set APP_ENV=development. To run a production build, build the binary and run it directly.`).
		WithCode("dev.production_environment").
		WithDetail("app_env", raw).
		WithDocs(devDocs)
}

// validateDevPorts rejects an unusable port and reports a port that is
// already held, naming the flag that moves it.
func validateDevPorts(f devFlags) error {
	checks := []struct {
		role, flag, port string
		skip             bool
	}{
		{role: "proxy", flag: "--port", port: f.port, skip: f.embedded},
		{role: "app", flag: "--app-port", port: appListenPort(f)},
		{role: "vite", flag: "--vite-port", port: f.vitePort, skip: f.embedded},
		// The database port is validated but never probed for occupancy.
		// The normal path reuses a container that is already listening on
		// it, so a "port in use" refusal here would reject the common
		// case. A port held by something that is *not* this project's
		// container is caught instead by the container runtime's own
		// refusal to publish it, and by VerifyPublishedPort.
		{role: "database", flag: "--db-port", port: f.dbPort, skip: true},
	}
	for _, c := range checks {
		if err := dev.ValidatePort(c.role, c.port); err != nil {
			return clierr.Wrap(err, clierr.ExitUsage,
				err.Error(), fmt.Sprintf("Pass %s a number between 1 and 65535.", c.flag)).
				WithDocs(devDocs)
		}
	}
	if f.graceMs < 100 || f.graceMs > 120_000 {
		return clierr.Usage(
			fmt.Sprintf("--stop-grace must be between 100 and 120000 milliseconds, found %d", f.graceMs),
			"See docs/commands/dev.md for why the default is longer than the application's own drain period.").
			WithDocs(devDocs)
	}
	if f.pollMilli < 20 || f.pollMilli > 60_000 {
		return clierr.Usage(
			fmt.Sprintf("--poll must be between 20 and 60000 milliseconds, found %d", f.pollMilli),
			"A shorter interval spends CPU for latency the rebuild swallows anyway; see docs/commands/dev.md.").
			WithDocs(devDocs)
	}
	for _, c := range checks {
		if c.skip {
			continue
		}
		if err := dev.CheckPortFree(c.role, "127.0.0.1", c.port); err != nil {
			return portError(c.role, c.flag, c.port, err)
		}
	}
	return nil
}

// portError renders a taken port as a precondition error naming the flag
// that moves it and the command that finds its holder.
func portError(role, flagName, port string, err error) error {
	var inUse *dev.PortInUseError
	if errors.As(err, &inUse) {
		return clierr.Wrap(err, clierr.ExitPrecondition, inUse.Error(), inUse.Recovery(flagName)).
			WithCode("dev.port_in_use").
			WithDetail("port", port).
			WithDetail("role", role).
			WithDocs(devDocs)
	}
	return clierr.Wrap(err, clierr.ExitPrecondition,
		fmt.Sprintf("could not bind the %s port %s", role, port),
		fmt.Sprintf("Pass %s to choose another.", flagName)).WithDocs(devDocs)
}

// appListenPort is the port the Go child binds. In --embedded mode there is
// no proxy in front of it, so it takes the port the developer opens.
func appListenPort(f devFlags) string {
	if f.embedded {
		return f.port
	}
	return f.appPort
}

// buildDevProxy assembles the single-origin proxy.
func buildDevProxy(f devFlags, pr devPrinter) (*dev.Proxy, error) {
	appURL, err := url.Parse("http://127.0.0.1:" + f.appPort)
	if err != nil {
		return nil, clierr.Wrap(err, clierr.ExitUsage, "the app port is not usable in a URL", "Pass --app-port a plain port number.")
	}
	webURL, err := url.Parse("http://127.0.0.1:" + f.vitePort)
	if err != nil {
		return nil, clierr.Wrap(err, clierr.ExitUsage, "the vite port is not usable in a URL", "Pass --vite-port a plain port number.")
	}
	proxy, err := dev.NewProxy(dev.ProxyConfig{
		App: appURL,
		Web: webURL,
		OnError: func(up dev.Upstream, reason string) {
			pr.say(sourceDev, string(up)+" upstream unreachable: "+reason)
		},
	})
	if err != nil {
		return nil, clierr.Wrap(err, clierr.ExitError, "could not build the development proxy", "This is a defect; please report it.").WithDocs(devDocs)
	}
	return proxy, nil
}

// requireFrontendDependencies refuses to start Vite when the frontend has
// never been installed, rather than running pnpm on the developer's behalf.
// `nise new` says plainly that generation runs nothing; `nise dev` keeps
// that promise, and an explicit precondition error is more useful than a
// silent multi-minute install nobody asked for.
func requireFrontendDependencies(root string) error {
	for _, rel := range []string{"frontend/package.json", "frontend/node_modules"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return clierr.Precondition(
				fmt.Sprintf("%s is missing", rel),
				`Run "pnpm --dir frontend install" first, or pass --embedded to serve the built frontend from the Go binary instead.`).
				WithCode("dev.frontend_not_installed").WithDocs(devDocs)
		}
	}
	return nil
}
