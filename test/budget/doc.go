// Package budget holds the deterministic performance gates.
//
// The distinction this package is built around is between measurements that
// are reproducible on a shared CI runner and measurements that are not.
//
// Wall-clock time is not. A GitHub-hosted runner shares a physical machine
// with other jobs, its CPU frequency is not ours to know, and the same commit
// measured twice can differ by a factor of three. A gate on it fails for
// reasons that have nothing to do with the change, and a gate that fails for
// unrelated reasons is one people learn to re-run until it passes — which is
// worse than no gate, because it also teaches them to re-run the ones that
// matter.
//
// Allocation counts, byte sizes, and database round trips are reproducible.
// They are properties of the code rather than of the machine, they change only
// when somebody changes something, and a change in one of them is a fact worth
// a sentence in a commit message. Those are what this package gates.
//
// Everything else — latency, throughput, memory under load — is measured and
// recorded in docs/performance-baselines.md, and reviewed by a person. See
// privateDocs/PERFORMANCE_BUDGETS.md for the full policy.
package budget
