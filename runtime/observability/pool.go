package observability

// PoolStats is a snapshot of one database connection pool's state.
// [runtime/config] and [runtime/lifecycle] exist; the pool package the
// blueprint describes for M3 (pgxpool through sqlc, blueprint §7) does
// not. PoolStats is the seam that future pool package is expected to
// satisfy — most directly by adapting *pgxpool.Pool.Stat() — so M3 has a
// fixed contract to build against. This package does not fabricate a pool
// implementation to have something to measure; pool_test.go exercises this
// seam with a hand-written fake instead.
type PoolStats struct {
	// MaxConns is the pool's configured maximum connection count.
	MaxConns int32
	// OpenConns is the number of connections currently open, idle or in
	// use.
	OpenConns int32
	// IdleConns is the number of open connections not currently checked
	// out.
	IdleConns int32
	// InUseConns is the number of open connections currently checked out.
	InUseConns int32
}

// PoolStatsFunc returns the current [PoolStats] for one connection pool.
// [PoolMetrics.Register] calls it at exposition time, on every scrape, not
// on a push schedule: a pool already tracks these counts internally, so
// there is nothing for this package to accumulate independently. A
// PoolStatsFunc must be safe for concurrent use.
type PoolStatsFunc func() PoolStats

// PoolMetrics holds four gauges describing a database connection pool's
// state — maximum, open, idle, and in-use connection counts — each labeled
// by pool name. Construct one with [NewPoolMetrics], then call
// [PoolMetrics.Register] once per pool the application constructs (a
// primary pool, and optionally others such as a read replica).
type PoolMetrics struct {
	maxConns   *gaugeFuncVec
	openConns  *gaugeFuncVec
	idleConns  *gaugeFuncVec
	inUseConns *gaugeFuncVec
}

// NewPoolMetrics registers PoolMetrics' four gauges on reg:
// "db_pool_max_conns", "db_pool_open_conns", "db_pool_idle_conns", and
// "db_pool_in_use_conns", each labeled by "pool".
func NewPoolMetrics(reg *Registry) (*PoolMetrics, error) {
	maxConns, err := reg.newGaugeFuncVec(VecOpts{
		Name:   "db_pool_max_conns",
		Help:   "Maximum connections configured for the pool.",
		Labels: []string{"pool"},
	})
	if err != nil {
		return nil, err
	}

	openConns, err := reg.newGaugeFuncVec(VecOpts{
		Name:   "db_pool_open_conns",
		Help:   "Connections currently open in the pool, idle or in use.",
		Labels: []string{"pool"},
	})
	if err != nil {
		return nil, err
	}

	idleConns, err := reg.newGaugeFuncVec(VecOpts{
		Name:   "db_pool_idle_conns",
		Help:   "Open connections currently idle.",
		Labels: []string{"pool"},
	})
	if err != nil {
		return nil, err
	}

	inUseConns, err := reg.newGaugeFuncVec(VecOpts{
		Name:   "db_pool_in_use_conns",
		Help:   "Open connections currently checked out and in use.",
		Labels: []string{"pool"},
	})
	if err != nil {
		return nil, err
	}

	return &PoolMetrics{
		maxConns:   maxConns,
		openConns:  openConns,
		idleConns:  idleConns,
		inUseConns: inUseConns,
	}, nil
}

// Register wires statsFunc into the four pool gauges under poolName.
// poolName distinguishes multiple pools in exposition output and must be
// unique per PoolMetrics — a startup-time decision bounded by how many
// pools the application constructs, never by request-controlled input.
// Call it once at startup for each pool. statsFunc is invoked on every
// scrape and must be safe for concurrent use.
func (m *PoolMetrics) Register(poolName string, statsFunc PoolStatsFunc) error {
	if err := m.maxConns.add(func() float64 { return float64(statsFunc().MaxConns) }, poolName); err != nil {
		return err
	}
	if err := m.openConns.add(func() float64 { return float64(statsFunc().OpenConns) }, poolName); err != nil {
		return err
	}
	if err := m.idleConns.add(func() float64 { return float64(statsFunc().IdleConns) }, poolName); err != nil {
		return err
	}
	if err := m.inUseConns.add(func() float64 { return float64(statsFunc().InUseConns) }, poolName); err != nil {
		return err
	}
	return nil
}
