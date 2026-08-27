# Operations: health probes, graceful shutdown, and process modes

`runtime/health` and `runtime/lifecycle` ([ADR 0011](adr/0011-runtime-public-api.md)) are how a generated application exposes its health to an orchestrator or load balancer, shuts down without dropping in-flight work, and runs as one, some, or all of its process components. This page documents the current, implemented behavior — it is not a design proposal.

## The probe contract

Three endpoints, three distinct meanings. A generated application mounts each of `runtime/health`'s handlers at whatever path its own router uses — this package builds `net/http.Handler` values, not routes — but the conventional paths below match common orchestrator defaults and are what `nise`'s generated router uses.

| Probe | Suggested path | Built with | Wire it to |
|---|---|---|---|
| Startup | `/healthz/startup` | `health.NewStartupHandler(gate)` | Your orchestrator's startup probe. Nothing else should be checked, including liveness and readiness, until this passes. |
| Liveness | `/healthz/live` | `health.NewLivenessHandler(gate)` | Your orchestrator's liveness probe, whose failure **restarts** the process. Only fail this for a condition a restart can actually fix (a detected deadlock), never because a downstream dependency is unavailable. |
| Readiness | `/healthz/ready` | `health.NewReadinessHandler(gate, prober)` | Your load balancer's health check, whose failure **stops new traffic being routed here** without restarting anything. |

### Status codes and response bodies

Every one of the three handlers above answers an unauthenticated caller with a status code and one of exactly two fixed JSON bodies:

| Status | Body |
|---|---|
| `200 OK` | `{"status":"ok"}` |
| `503 Service Unavailable` | `{"status":"unavailable"}` |

That is all an unauthenticated caller ever learns. No dependency name, no error string, no timing, and no distinction between "the database is down" and "the cache is down" ever reaches the response — a readiness endpoint that names its failing dependency and where it tried to reach it hands an unauthenticated caller your internal topology for free.

### The detailed view

`health.NewReadinessReportHandler(gate, prober)` is the seam for the opposite case: a per-check JSON report — each registered check's name, whether it passed, its error text, and how long it took. **This package implements no authorization of its own.** Mounting this handler is only safe behind middleware that authenticates the caller first (an internal-only network path, a bearer token checked by your own code, mutual TLS — whatever your application already uses). Never mount it at a path an unauthenticated caller can reach.

### Registering checks

A `health.Prober` is built once, from an explicit `[]health.Check` passed to `health.NewProber`, each naming a dependency and a `health.CheckFunc`:

```go
prober := health.NewProber([]health.Check{
    {Name: "database", Func: func(ctx context.Context) error {
        return db.PingContext(ctx)
    }},
    {Name: "cache", Func: func(ctx context.Context) error {
        return cache.Ping(ctx)
    }},
})
```

There is no registry populated by import or side effect: a check that is not in this slice is not run, and the full list an application depends on is visible at this one call site.

### Timeouts: a hung dependency cannot hang the probe

`Prober.Probe` enforces two independent bounds:

| Bound | Default | Overridable with |
|---|---|---|
| Per-check timeout | `health.DefaultCheckTimeout` = 2s | `health.WithCheckTimeout` |
| Overall deadline, across every check together | `health.DefaultOverallTimeout` = 5s | `health.WithOverallTimeout` |

Both bounds hold even against a check that ignores its `context.Context` and never returns: `Probe` always returns within the overall deadline, reporting that check as failed with a fixed `"check timed out"` message. The one cost, inherent to Go and not something this package can avoid, is that such a check leaks the goroutine running it for the life of the process — write checks that respect `ctx.Done()`.

## The shutdown sequence

`lifecycle.HTTPServer.Shutdown` runs, in order:

1. **Mark the readiness gate not ready.** `health.Gate.Set(false)`, so `health.NewReadinessHandler` starts answering `503` immediately.
2. **Wait the drain period.** `lifecycle.DefaultDrainPeriod` = 5s, overridable with `lifecycle.WithDrainPeriod`.
3. **Stop accepting new connections and wait for in-flight requests**, bounded by the shutdown timeout: `lifecycle.DefaultShutdownTimeout` = 30s, overridable with `lifecycle.WithShutdownTimeout`.
4. **Force close** anything still in flight once that bound elapses, and return an error wrapping `lifecycle.ErrShutdownTimeout`. A `nil` return means every in-flight request finished on its own — the distinction between the two is exactly the signal an operator needs to tell a clean shutdown from a forced one.

### Why the drain period exists, and comes first

A load balancer or ingress does not learn that an instance has failed its readiness check the instant that instance decides to fail it — it learns on its own polling interval, commonly every one to ten seconds depending on the product. If step 3 happened immediately after step 1, any request the load balancer sent in the gap between "we marked ourselves not ready" and "the load balancer's next poll observes that" would arrive at a listener that has already stopped accepting, and be refused or reset instead of served.

Waiting a drain period **between** failing readiness and actually stopping the listener gives the load balancer's own next poll time to happen and be acted on first. Five seconds is chosen to comfortably cover one poll interval for the great majority of default load balancer and ingress configurations; a deployment whose health-check interval is longer than that should raise `lifecycle.WithDrainPeriod` to match it (a reasonable rule of thumb is at least two poll intervals). This ordering — readiness fails, *then* wait, *then* stop accepting — is easy to get backwards while still looking correct in a quick manual test, because a manual test rarely has a load balancer in the loop polling on its own schedule to expose the gap.

### Worker shutdown

`lifecycle.Worker.Shutdown` cancels the `context.Context` the worker's loop was given and waits for the loop to return, bounded by the caller's own deadline; past that bound it returns an error wrapping `lifecycle.ErrShutdownTimeout`, the same sentinel `HTTPServer` uses. Both `HTTPServer.Shutdown` and `Worker.Shutdown` are idempotent and safe to call before the corresponding `Run`.

### No signal handling in `runtime/lifecycle`

This package never calls `signal.Notify`. Deciding when to shut down — typically on `SIGINT` or `SIGTERM` — is the generated application's job, in its own `main`:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

group, err := lifecycle.NewGroup(mode, webServer, worker)
if err != nil {
    return err
}

runErr := make(chan error, 1)
go func() { runErr <- group.Run(context.Background()) }()

select {
case <-ctx.Done():
    shutdownCtx, cancel := context.WithTimeout(context.Background(), lifecycle.DefaultShutdownTimeout)
    defer cancel()
    return group.Shutdown(shutdownCtx)
case err := <-runErr:
    return err
}
```

## Process modes

`lifecycle.Mode` is a closed, typed set of exactly three values, parsed by `lifecycle.ParseMode` (an unrecognized value fails closed, the same policy `runtime/config.ParseEnvironment` uses for `Environment`):

| Mode | Web (HTTP server) | Worker | HTTP port bound |
|---|---|---|---|
| `all` | started | started | yes |
| `web` | started | **not started** | yes |
| `worker` | **not started** | started | **no** |

`lifecycle.NewGroup(mode, web, worker)` builds exactly the component set the table above describes — `ModeWeb` never even holds a reference to a worker component to start, and `ModeWorker` never calls `net.Listen` at all, so the HTTP port is never bound, not merely unserved.

### Failure in one component tears down the other

Under `ModeAll`, if either component's `Run` returns — on a startup failure such as a bind error, or any other reason — before the other has been asked to stop, `Group.Run` shuts the other down (bounded by the group's own shutdown timeout) before returning. The error `Group.Run` returns is the first non-nil error observed, named by which component produced it. A generated application never ends up with, for example, a worker still processing jobs after its web server has crashed and exited: the whole process's `Run` call does not return until every component has actually stopped.
