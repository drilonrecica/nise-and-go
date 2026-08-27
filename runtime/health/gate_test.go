package health

import (
	"sync"
	"testing"
)

func TestGate_InitialState(t *testing.T) {
	if !NewGate(true).Ready() {
		t.Error("NewGate(true).Ready() = false, want true")
	}
	if NewGate(false).Ready() {
		t.Error("NewGate(false).Ready() = true, want false")
	}
}

func TestGate_Set(t *testing.T) {
	g := NewGate(true)
	g.Set(false)
	if g.Ready() {
		t.Error("Ready() = true after Set(false)")
	}
	g.Set(true)
	if !g.Ready() {
		t.Error("Ready() = false after Set(true)")
	}
}

// TestGate_ConcurrentReadWrite exercises Gate under concurrent readers and
// writers with -race: it must never race, regardless of the outcome of any
// individual Set/Ready ordering.
func TestGate_ConcurrentReadWrite(t *testing.T) {
	g := NewGate(true)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			g.Set(i%2 == 0)
		}(i)
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = g.Ready()
		}()
	}
	wg.Wait()
}
