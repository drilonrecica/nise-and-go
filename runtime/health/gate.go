package health

import "sync"

// Gate is a concurrency-safe on/off switch behind the startup and liveness
// probes, and behind the manual portion of the readiness probe. A process's
// lifecycle code flips a Gate's state as initialization finishes and as
// shutdown begins; this package's handlers only ever read it.
//
// A Gate is safe for concurrent use by multiple goroutines, including
// concurrent calls to [Gate.Set] and [Gate.Ready].
type Gate struct {
	mu    sync.RWMutex
	ready bool
}

// NewGate returns a Gate whose initial state is ready.
func NewGate(ready bool) *Gate {
	return &Gate{ready: ready}
}

// Set updates the Gate's state.
func (g *Gate) Set(ready bool) {
	g.mu.Lock()
	g.ready = ready
	g.mu.Unlock()
}

// Ready reports the Gate's current state.
func (g *Gate) Ready() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.ready
}
