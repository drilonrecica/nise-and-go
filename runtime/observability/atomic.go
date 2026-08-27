package observability

import (
	"math"
	"sync/atomic"
)

// atomicFloat64 is a lock-free float64 accumulator built on
// [atomic.Uint64] and a compare-and-swap retry loop, the standard pattern
// for a concurrent float64 counter in Go: there is no atomic float64 add in
// the standard library, but the bit pattern of a float64 can be compared
// and swapped like any other 64-bit value.
type atomicFloat64 struct {
	bits atomic.Uint64
}

// add atomically adds delta to the current value.
func (f *atomicFloat64) add(delta float64) {
	for {
		old := f.bits.Load()
		next := math.Float64bits(math.Float64frombits(old) + delta)
		if f.bits.CompareAndSwap(old, next) {
			return
		}
	}
}

// store atomically replaces the current value with v.
func (f *atomicFloat64) store(v float64) {
	f.bits.Store(math.Float64bits(v))
}

// load atomically reads the current value.
func (f *atomicFloat64) load() float64 {
	return math.Float64frombits(f.bits.Load())
}
