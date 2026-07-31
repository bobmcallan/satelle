package mirror

import "time"

// Reconciliation timings live HERE, in the one package serve, web and the CLI
// diagnostics all import (serve → web → mirror; web can never import serve), so
// the loop that re-asks and the badge that reports a mirror it could not repair
// read the same numbers (sty_e6e467fe).
const (
	// DefaultReconcileInterval is how often the serving tier re-requests a full
	// snapshot for each partition it renders. The push path is the fast path;
	// this only bounds how long a DROPPED push can survive, so it is measured in
	// minutes rather than seconds — one re-seed costs a CLI process and a full
	// snapshot per partition.
	DefaultReconcileInterval = 5 * time.Minute
	// StaleAfter is how long a partition may go without a confirmed ingest
	// before a view must say so rather than presenting its frame as current.
	// 3× the interval so one slow or briefly-failed pass never flaps the badge.
	StaleAfter = 3 * DefaultReconcileInterval
)

// LastIngest parses the partition's last confirmed ingest time. ok is false when
// the stored value is absent or unparseable — callers then have no basis to
// claim freshness.
func (p Partition) LastIngest() (time.Time, bool) {
	if p.UpdatedAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, p.UpdatedAt)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Stale reports whether this partition has gone longer than StaleAfter without a
// confirmed ingest. An unparseable/absent timestamp counts as stale: an unknown
// last-ingest time is exactly the case the operator must not read as current.
func (p Partition) Stale(now time.Time) bool {
	t, ok := p.LastIngest()
	if !ok {
		return true
	}
	return now.UTC().Sub(t) > StaleAfter
}
