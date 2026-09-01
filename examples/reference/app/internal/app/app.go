// Generated once by nise; owned by this application. Nise will not overwrite it.

// Package app wires this application's components together and runs the
// process.
//
// Every dependency is constructed here, in Run, and passed to whatever
// needs it as a parameter. There is no container, no service locator, no
// registry populated by an import side effect, and no package-level mutable
// state: the whole object graph is readable top to bottom in one function.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	rtconfig "github.com/drilonrecica/nise-and-go/runtime/config"
	"github.com/drilonrecica/nise-and-go/runtime/health"
	"github.com/drilonrecica/nise-and-go/runtime/lifecycle"
	"github.com/drilonrecica/nise-and-go/runtime/logging"
	"github.com/drilonrecica/nise-and-go/runtime/observability"
	"github.com/drilonrecica/nise-and-go/runtime/pagination"

	migrationdb "workbench/db"
	"workbench/internal/features/audit"
	"workbench/internal/features/auth"
	"workbench/internal/features/organizations"
	"workbench/internal/features/reservation"
	"workbench/internal/features/resource"
	"workbench/internal/features/uploads"
	"workbench/internal/platform/authorization"
	"workbench/internal/platform/config"
	"workbench/internal/platform/database"
	"workbench/internal/platform/httpapi"
	"workbench/internal/platform/httpapi/problem"
	"workbench/internal/platform/httpauth"
	"workbench/internal/platform/jobs"
	"workbench/internal/platform/mail"
	"workbench/internal/platform/passwords"
	"workbench/internal/platform/reauth"
	"workbench/internal/platform/throttle"
	"workbench/internal/platform/webui"
)

// Run starts the application and blocks until it stops.
//
// ctx carries the shutdown signal: main installs the signal handler and
// cancels ctx, because runtime/lifecycle deliberately installs none of its
// own. modeOverride is the -mode flag's value; an empty string means the
// APP_MODE environment variable decides.
func Run(ctx context.Context, modeOverride string) error {
	cfg, warnings, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	logger := newLogger(cfg)
	for _, w := range warnings {
		logger.Warn("configuration warning", slog.String("detail", w))
	}

	mode, err := resolveMode(cfg, modeOverride)
	if err != nil {
		return err
	}

	registry := observability.NewRegistry()
	queryMetrics, err := database.NewQueryMetrics(registry)
	if err != nil {
		return fmt.Errorf("registering database query metrics: %w", err)
	}
	databaseRuntime, err := openDatabaseRuntime(ctx, cfg, logger, queryMetrics)
	if err != nil {
		return fmt.Errorf("starting database: %w", err)
	}
	defer databaseRuntime.Close()
	pool := databaseRuntime.Pool
	compatibility, err := database.CheckCompatibility(ctx, pool, migrationdb.Migrations())
	if err != nil {
		return fmt.Errorf("checking database schema compatibility: %w", err)
	}
	if compatibility.State != database.SchemaCurrent {
		logger.Warn("database schema is behind the embedded migration head",
			slog.String("state", string(compatibility.State)),
			slog.Int64("current_version", compatibility.Current),
			slog.Int64("target_version", compatibility.Target),
			slog.String("fix", "run the explicit database migration command before enabling features that need the pending schema"))
	}
	// Row-level security is only a boundary if it applies to this role. A
	// superuser — or any role with BYPASSRLS — bypasses every policy, and
	// FORCE ROW LEVEL SECURITY does not change that: it removes the table
	// owner's exemption, not the superuser's. A deployment connecting as
	// `postgres` therefore has tenant isolation that looks complete in the
	// schema and enforces nothing.
	isolation, err := database.CheckTenantIsolation(ctx, pool)
	if err != nil {
		return fmt.Errorf("checking tenant isolation: %w", err)
	}
	if !isolation.Enforced() {
		detail := fmt.Sprintf("row-level security does not apply to the database role %q: %s", isolation.Role, isolation.Reason())
		if cfg.Env == rtconfig.Production {
			return fmt.Errorf("%s; connect as a role that is neither a superuser nor BYPASSRLS", detail)
		}
		logger.Warn("tenant isolation is not enforced",
			slog.String("detail", detail),
			slog.String("fix", "connect as a role that is neither a superuser nor BYPASSRLS; production refuses to start without one"))
	}

	// Fail closed in production: a production binary with no frontend
	// build embedded would serve the development placeholder to real
	// users, so it refuses to start instead.
	ui, err := webui.New(cfg.Env == rtconfig.Production)
	if err != nil {
		return fmt.Errorf("loading the embedded frontend: %w", err)
	}
	if !ui.Built() {
		logger.Warn("serving the placeholder page: no frontend build is embedded",
			slog.String("fix", "pnpm --dir frontend install && pnpm --dir frontend build"))
	}

	poolMetrics, err := observability.NewPoolMetrics(registry)
	if err != nil {
		return fmt.Errorf("registering database pool metrics: %w", err)
	}
	if err := poolMetrics.Register("primary", func() observability.PoolStats {
		return database.Stats(pool)
	}); err != nil {
		return fmt.Errorf("registering primary database pool: %w", err)
	}
	httpMetrics, err := observability.NewHTTPMetrics(registry)
	if err != nil {
		return fmt.Errorf("registering HTTP metrics: %w", err)
	}

	// The gate starts closed so that the startup and readiness probes both
	// report unavailable until wiring has finished.
	gate := health.NewGate(false)

	prober := health.NewProber([]health.Check{database.ReadinessCheck(pool)})

	cursors, err := pagination.NewCodec(cfg.CursorKeys, cfg.CursorTTL)
	if err != nil {
		return fmt.Errorf("building the pagination cursor codec: %w", err)
	}

	transactor, err := database.NewTransactor(pool)
	if err != nil {
		return fmt.Errorf("building the transaction runner: %w", err)
	}
	sessions, err := auth.NewSessions(transactor, cfg.SessionLifetime)
	if err != nil {
		return fmt.Errorf("building the session use case: %w", err)
	}
	accounts, err := auth.NewAccounts(transactor)
	if err != nil {
		return fmt.Errorf("building the account use case: %w", err)
	}
	passwordPolicy, err := passwords.Policy()
	if err != nil {
		return fmt.Errorf("building the password policy: %w", err)
	}
	auditRecorder, err := audit.NewRecorder(transactor)
	if err != nil {
		return fmt.Errorf("building the audit recorder: %w", err)
	}
	loginThrottle, err := throttle.NewLimiter(transactor, cfg.LoginThrottle)
	if err != nil {
		return fmt.Errorf("building the authentication throttle: %w", err)
	}
	// The second factor comes from the generated module wiring: the optional
	// TOTP module when it was selected, and an explicit "this application has
	// none" when it was not.
	// The issuer is the name an authenticator application shows beside the
	// account. It is the generated project's name, and this file is
	// application-owned: change it here if the product is called something
	// else.
	secondFactor, err := SecondFactor(transactor, auditRecorder, "workbench")
	if err != nil {
		return err
	}
	credentials, err := auth.NewCredentials(accounts, sessions, passwordPolicy, loginThrottle, auditRecorder, secondFactor)
	if err != nil {
		return fmt.Errorf("building the login use case: %w", err)
	}
	compromisedPasswords, err := passwords.Compromised()
	if err != nil {
		return fmt.Errorf("loading the known-compromised password list: %w", err)
	}
	sessionCookies, err := httpauth.NewCookies(cfg.SessionCookie)
	if err != nil {
		return fmt.Errorf("building the session cookie boundary: %w", err)
	}
	sessionResolver, err := httpauth.NewResolver(sessions, sessionCookies)
	if err != nil {
		return fmt.Errorf("building the session resolver: %w", err)
	}
	permissionCatalog, err := authorization.Catalog()
	if err != nil {
		return fmt.Errorf("building the permission catalog: %w", err)
	}
	roles, err := auth.NewRoles(transactor, permissionCatalog)
	if err != nil {
		return fmt.Errorf("building the role use case: %w", err)
	}
	// The session source is injected rather than imported, so the
	// authorization and session packages do not become mutually dependent the
	// moment one wants something from the other.
	authorizationResolver, err := authorization.NewResolver(roles, permissionCatalog, func(ctx context.Context) (string, bool) {
		record, ok := httpauth.FromContext(ctx)
		return record.UserID, ok
	})
	if err != nil {
		return fmt.Errorf("building the authorization resolver: %w", err)
	}

	// The reauthentication matrix is code for the same reason the permission
	// catalog is: an action's meaning is decided by the use case that checks
	// it, and every replica has to agree about which actions need a proof.
	reauthMatrix, err := reauth.DefaultMatrix()
	if err != nil {
		return fmt.Errorf("building the reauthentication matrix: %w", err)
	}
	reauthResolver, err := reauth.NewResolver(reauthMatrix, httpauth.FromContext)
	if err != nil {
		return fmt.Errorf("building the reauthentication resolver: %w", err)
	}

	originPolicy, err := httpauth.NewOriginPolicy(cfg.AllowedOrigins)
	if err != nil {
		return fmt.Errorf("building the allowed-origin policy: %w", err)
	}
	// Two guards over one policy: each surface refuses in its own format, so
	// neither package has to know about the other's.
	apiGuard, err := httpauth.NewGuard(originPolicy, sessionCookies, problem.HTTPHandler(problem.CrossSiteRequest()))
	if err != nil {
		return fmt.Errorf("building the API anti-forgery guard: %w", err)
	}
	documentGuard, err := httpauth.NewGuard(originPolicy, sessionCookies, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	if err != nil {
		return fmt.Errorf("building the document anti-forgery guard: %w", err)
	}
	invitations, err := auth.NewInvitations(transactor, credentials, roles, compromisedPasswords, auth.DefaultInvitationLifetime)
	if err != nil {
		return fmt.Errorf("building the invitation use case: %w", err)
	}

	// Mail is chosen once, here, so nothing downstream branches on which
	// transport is in use. Production has already refused the log transport
	// by this point; see config.Load.
	//
	// It is built before the API adapter rather than after it, because
	// Workbench's reservation use case sends mail through a job and the
	// adapter needs that use case. The framework's starter order had nothing
	// depending on mail, so it could be built later.
	mailer, err := newMailer(cfg, logger)
	if err != nil {
		return fmt.Errorf("building the mailer: %w", err)
	}
	mailRenderer, err := mail.NewRenderer()
	if err != nil {
		return fmt.Errorf("parsing the embedded message templates: %w", err)
	}
	appNotifications, err := newNotifications(transactor, mailer, mailRenderer, cfg.MailFrom, logger)
	if err != nil {
		return fmt.Errorf("building the notification use case: %w", err)
	}

	// Workbench's own use cases.
	//
	// The reservation use case is handed its collaborators rather than
	// reaching for them, and every one of them is called inside the
	// transaction that made a change: the audit record, the notification, and
	// the enqueued job become visible if and only if the change committed.
	// Passing them in here is what makes that arrangement visible from the
	// wiring instead of hidden inside the feature.
	//
	// The enqueuer is deferred because there is a real cycle: the use case
	// enqueues, the job client is built from a registry of workers, and the
	// workers need the use case. See deferredEnqueuer.
	tenants, err := organizations.New(transactor)
	if err != nil {
		return fmt.Errorf("building the organizations use case: %w", err)
	}
	resources, err := resource.New(transactor)
	if err != nil {
		return fmt.Errorf("building the resource use case: %w", err)
	}
	enqueuer := &deferredEnqueuer{}
	reservations, err := reservation.New(transactor, reservation.Options{
		Audit:  auditRecorder,
		Jobs:   enqueuer,
		Notify: appNotifications,
	})
	if err != nil {
		return fmt.Errorf("building the reservation use case: %w", err)
	}

	apiServer, err := httpapi.NewServer(httpapi.ServerDeps{
		Cursors:      cursors,
		Credentials:  credentials,
		Sessions:     sessions,
		Accounts:     accounts,
		Invitations:  invitations,
		Cookies:      sessionCookies,
		Compromised:  compromisedPasswords,
		Resources:    resources,
		Reservations: reservations,
		Tenancy:      tenants,
		Modules:      SelectedModules(),
		Environment:  string(cfg.Env),
	})
	if err != nil {
		return fmt.Errorf("building the API adapter: %w", err)
	}

	registerAPI := apiServer.Register
	notificationStream, err := newNotificationStream(cfg, pool, logger)
	if err != nil {
		return fmt.Errorf("building the notification stream: %w", err)
	}
	registerAPI, err = notificationRoutes(registerAPI, notificationStream, appNotifications)
	if err != nil {
		return fmt.Errorf("mounting the notification stream: %w", err)
	}

	handler, err := httpapi.New(httpapi.Deps{
		Config:      cfg,
		Logger:      logger,
		Gate:        gate,
		Prober:      prober,
		Registry:    registry,
		Metrics:     httpMetrics,
		UI:          ui,
		RegisterAPI: registerAPI,
		Middleware: httpapi.MiddlewareSlots{
			// Resolving the session is a request-identity concern, so it
			// runs on both surfaces: the document chain needs it as soon as
			// the frontend has protected pages, and running it in one place
			// is what keeps the two from disagreeing about who is asking.
			//
			// The anti-forgery guard runs inside the resolver, so the
			// session it checks the token against is the one already
			// resolved for this request.
			// Authority is resolved after the session it belongs to and
			// before the guard, so one request resolves it once and every
			// use case behind the handler sees the same answer. The
			// reauthentication position is resolved alongside it, from the
			// same session, so "may you" and "is it still you" cannot be
			// answered from two different reads.
			API:      []httpapi.Middleware{sessionResolver.Middleware, authorizationResolver.Middleware, reauthResolver.Middleware, apiGuard.Middleware},
			Document: []httpapi.Middleware{sessionResolver.Middleware, authorizationResolver.Middleware, reauthResolver.Middleware, documentGuard.Middleware},
		},
	})
	if err != nil {
		return fmt.Errorf("building the HTTP handler: %w", err)
	}

	// Every process builds a job client, because every process may need to
	// enqueue. Only the worker component ever starts one, so a web replica
	// holds a client that never fetches a job.
	objectStore, closeStore, err := newStore(cfg)
	if err != nil {
		return fmt.Errorf("building the object store: %w", err)
	}
	defer func() { _ = closeStore() }()
	uploadLifecycle, err := uploads.New(transactor, objectStore)
	if err != nil {
		return fmt.Errorf("building the upload lifecycle: %w", err)
	}

	jobRegistry := jobs.NewRegistry()
	if err := registerJobs(jobRegistry, uploadLifecycle, appNotifications, logger); err != nil {
		return fmt.Errorf("registering background jobs: %w", err)
	}
	if err := registerReservationJobs(jobRegistry, reservations, accounts, mailer, cfg.MailFrom, logger); err != nil {
		return fmt.Errorf("registering reservation jobs: %w", err)
	}
	jobClient, err := jobs.New(pool, jobRegistry, cfg.Jobs, logger)
	if err != nil {
		return fmt.Errorf("building the job client: %w", err)
	}
	// The cycle closes here. Until this line an enqueue would fail loudly and
	// roll its transaction back, which is the right failure: a job silently
	// dropped for a change that did commit is the one thing the transactional
	// enqueue exists to prevent.
	enqueuer.delegate = jobClient

	server := lifecycle.NewHTTPServer(cfg.Addr(), handler, gate, lifecycle.WithLogger(logger))
	worker := newWorker(jobClient, logger)

	group, err := lifecycle.NewGroup(mode, server, worker)
	if err != nil {
		return fmt.Errorf("selecting process components: %w", err)
	}

	// The live-update listener belongs to whichever process serves the
	// endpoint, because the fan-out is in-process: a subscriber registered
	// by an HTTP handler is only woken by the listener in that same
	// process. It therefore runs wherever the HTTP handler does, and is
	// joined on the way out. See runNotificationStream.
	streamDone := make(chan struct{})
	streamCtx, stopStream := context.WithCancel(context.WithoutCancel(ctx))
	defer func() {
		stopStream()
		<-streamDone
	}()
	if mode == lifecycle.ModeAll || mode == lifecycle.ModeWeb {
		go func() {
			defer close(streamDone)
			runNotificationStream(streamCtx, notificationStream, logger)
		}()
	} else {
		close(streamDone)
	}

	logger.Info("starting",
		slog.String("mode", string(mode)),
		slog.String("env", string(cfg.Env)),
		slog.String("addr", cfg.Addr()),
		slog.Bool("frontend_embedded", ui.Built()),
		slog.Any("cursor_keys", cfg.CursorKeys),
		slog.String("session_cookie", cfg.SessionCookie.Name()),
		slog.Any("modules", SelectedModules()),
		slog.Any("job_kinds", jobClient.Kinds()),
		slog.Any("periodic_job_kinds", jobClient.PeriodicKinds()),
		slog.String("mail_transport", cfg.MailTransport),
		slog.Any("notification_channels", appNotifications.Channels()),
		slog.Bool("notifications_sse", cfg.NotificationsSSE),
		slog.String("storage_backend", cfg.StorageBackend),
		slog.Any("mail_templates", mailRenderer.Names()),
	)

	// Initialization is complete: open the gate so the startup probe
	// passes and the readiness probe starts consulting the prober.
	gate.Set(true)

	runErr := make(chan error, 1)
	go func() { runErr <- group.Run(context.WithoutCancel(ctx)) }()

	select {
	case err := <-runErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining")
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lifecycle.DefaultShutdownTimeout)
		defer cancel()
		if err := group.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutting down: %w", err)
		}
		<-runErr
		logger.Info("stopped")
		return nil
	}
}

// newLogger builds the process logger. runtime/logging never constructs a
// logger, never reads an environment variable to choose a level, and never
// calls slog.SetDefault — that decision is made here, once, and the
// resulting logger is passed to everything that needs one.
func newLogger(cfg config.Config) *slog.Logger {
	opts := logging.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(logging.NewTextHandler(os.Stdout, logging.TextOptions{HandlerOptions: opts}))
	}
	return slog.New(logging.NewJSONHandler(os.Stdout, opts))
}
