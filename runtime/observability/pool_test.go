package observability

import (
	"strings"
	"sync/atomic"
	"testing"
)

// fakePool is a hand-written stand-in for the pgxpool-backed pool M3 will
// eventually build; it exists only to exercise the PoolStats seam, never
// to fabricate the pool package itself.
type fakePool struct {
	open atomic.Int32
}

func (p *fakePool) stats() PoolStats {
	return PoolStats{
		MaxConns:   10,
		OpenConns:  p.open.Load(),
		IdleConns:  p.open.Load() - 1,
		InUseConns: 1,
	}
}

func TestPoolMetricsSeamSamplesLiveStats(t *testing.T) {
	reg := NewRegistry()
	pm, err := NewPoolMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}

	pool := &fakePool{}
	pool.open.Store(3)
	if err := pm.Register("primary", pool.stats); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	if err := WriteText(&b, reg); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	if !strings.Contains(out, `db_pool_open_conns{pool="primary"} 3`) {
		t.Errorf("exposition missing open-conns sample:\n%s", out)
	}
	if !strings.Contains(out, `db_pool_max_conns{pool="primary"} 10`) {
		t.Errorf("exposition missing max-conns sample:\n%s", out)
	}

	// The gauge must sample the pool live, not the value at registration
	// time.
	pool.open.Store(9)
	b.Reset()
	if err := WriteText(&b, reg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `db_pool_open_conns{pool="primary"} 9`) {
		t.Errorf("exposition did not reflect the pool's live state:\n%s", b.String())
	}
}

func TestPoolMetricsSeamMultiplePools(t *testing.T) {
	reg := NewRegistry()
	pm, err := NewPoolMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}

	primary := &fakePool{}
	primary.open.Store(2)
	replica := &fakePool{}
	replica.open.Store(5)

	if err := pm.Register("primary", primary.stats); err != nil {
		t.Fatal(err)
	}
	if err := pm.Register("replica", replica.stats); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	if err := WriteText(&b, reg); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	if !strings.Contains(out, `db_pool_open_conns{pool="primary"} 2`) {
		t.Errorf("missing primary sample:\n%s", out)
	}
	if !strings.Contains(out, `db_pool_open_conns{pool="replica"} 5`) {
		t.Errorf("missing replica sample:\n%s", out)
	}
}

func TestPoolMetricsSeamDuplicatePoolNameRejected(t *testing.T) {
	reg := NewRegistry()
	pm, err := NewPoolMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}

	pool := &fakePool{}
	if err := pm.Register("primary", pool.stats); err != nil {
		t.Fatal(err)
	}
	if err := pm.Register("primary", pool.stats); err == nil {
		t.Fatal("expected an error registering the same pool name twice, got nil")
	}
}
