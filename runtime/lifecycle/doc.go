// Package lifecycle owns the shape of a generated application's process:
// which components run for a given process [Mode], how the HTTP server
// drains its connections before it stops accepting new ones, and how a
// worker loop is stopped in step with it.
//
// # No signal handling here
//
// This package installs no signal handler. Deciding when to shut down —
// typically on SIGINT or SIGTERM — is the application's job, done in its own
// main function with the standard library's [os/signal] package. This
// package exposes only the seam that decision needs: every [Runnable]'s Run
// and Shutdown take a [context.Context], and nothing in this package calls
// [os/signal.Notify] or otherwise reaches for a signal on its own. A
// runtime package that installed its own signal handler would be exactly
// the hidden lifecycle behavior this project's blueprint forbids.
//
// # Process modes
//
// [Mode] is a closed, typed set of three values — [ModeAll], [ModeWeb], and
// [ModeWorker] — parsed explicitly by [ParseMode], which fails on anything
// else rather than guessing. [NewGroup] builds the fixed set of components a
// [Mode] actually runs: [ModeWeb] never starts the worker, [ModeWorker]
// never binds the HTTP port, and [ModeAll] runs both and tears one down if
// the other fails.
//
// # Graceful shutdown and the drain period
//
// [HTTPServer] wraps [net/http.Server] with the ordering that makes a
// zero-downtime deploy possible: [HTTPServer.Shutdown] first marks a
// [runtime/health.Gate] not ready, then waits a configurable drain period
// ([DefaultDrainPeriod] by default), and only then stops accepting new
// connections and waits — bounded by [DefaultShutdownTimeout] — for
// in-flight requests to finish before forcing a close.
//
// That ordering matters because a load balancer or ingress observes
// readiness by polling it, on its own interval; it does not learn that an
// instance has stopped accepting the instant that instance decides to stop.
// If the listener closed the moment shutdown began, requests already in
// flight to this instance — sent before the load balancer's next poll could
// see the failure — would be refused or reset instead of completing. Failing
// readiness first, and waiting past at least one poll interval before the
// listener actually closes, is what lets the load balancer stop routing here
// before connections start being refused, instead of after.
//
// [Worker] gives a background-processing loop the same Run/Shutdown shape:
// Shutdown cancels the context the loop was given and waits, bounded by its
// own caller-supplied deadline, for the loop to return.
//
// Both [HTTPServer.Shutdown] and [Worker.Shutdown] distinguish a clean
// shutdown from one that had to force a close: see [ErrShutdownTimeout].
// Both are idempotent and safe to call before the corresponding Run.
package lifecycle
