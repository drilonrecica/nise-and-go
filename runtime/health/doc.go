// Package health provides the startup, liveness, and readiness probes a
// generated application exposes to its orchestrator, and the aggregation
// logic that keeps a hung dependency from hanging the readiness probe.
//
// # Three distinct probes
//
// The three probes answer three different questions, and an operator wires
// each to a different orchestrator behavior:
//
//   - Startup: has initialization finished (migrations checked, pools
//     built)? Wire it to a startup probe that gates when the other two
//     probes start being consulted at all.
//   - Liveness: is the process healthy enough to keep, or should it be
//     restarted? Wire it to a liveness probe whose failure restarts the
//     container.
//   - Readiness: should this instance receive traffic right now? Wire it to
//     a readiness probe (and to a load balancer's health check) whose
//     failure stops new traffic from being routed here without restarting
//     anything.
//
// [Gate] is the concurrency-safe on/off switch behind startup and liveness,
// and behind the manual portion of readiness; [Prober] is the aggregation of
// registered dependency checks behind the automatic portion of readiness.
// [NewStartupHandler], [NewLivenessHandler], and [NewReadinessHandler] build
// the three probes' [net/http.Handler] from those pieces.
//
// # No internal detail to an unauthenticated caller
//
// Every handler built by this package answers an unauthenticated caller with
// nothing but a status code and one of two fixed, minimal JSON bodies —
// never a dependency name, an error string, a duration, or anything else
// that would hand an attacker the shape of this instance's internal
// topology. [NewReadinessReportHandler] is the seam for the opposite case: a
// detailed, per-check [Report] for an authorized caller. This package
// implements no authorization itself — mount [NewReadinessReportHandler]
// only behind middleware that authenticates the caller first.
//
// # Checks are registered explicitly
//
// A [Prober] is built once, by its constructor, from an explicit slice of
// [Check] values the application passes in. There is no package-level
// registry and no side-effecting registration call: a check that is not
// passed to [NewProber] is not run, and the full set an application relies
// on is visible at one call site.
//
// # A hung dependency cannot hang the probe
//
// [Prober.Probe] bounds every check by two independent timeouts: a per-check
// timeout ([WithCheckTimeout], default [DefaultCheckTimeout]) and an overall
// deadline across every check together ([WithOverallTimeout], default
// [DefaultOverallTimeout]). Both bounds are enforced even against a check
// that ignores context cancellation and never returns: Probe always returns
// by the overall deadline, reporting that check as failed. The trade-off,
// inherent to Go and not something this package can avoid, is that a check
// which never respects its context leaks the goroutine running it for the
// life of the process; write checks that return promptly when their context
// is done.
package health
