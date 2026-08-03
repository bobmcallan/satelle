// Package lease persists an EXPLICIT, OWNED engagement seat for satelle work
// items (sty_8426b9c0). Engagement is no longer only derived from committed
// status: a lease is acquired at the START of an engaging transition (before
// gate+dispatch) so concurrent engages and edit/commit gates see the seat
// immediately. The DOT shape still defines WHICH states are engaging; the lease
// records WHO holds the seat NOW.
//
// Atomicity: store.Open uses MaxOpenConns(1) + _txlock=immediate, so a
// check-and-insert inside one BEGIN IMMEDIATE transaction is single-flight
// across concurrent satelle processes.
package lease

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// HeartbeatTTL is how long a lease may sit without a heartbeat before a
// colliding acquire treats it as stale (process death). Must exceed a long
// synchronous dispatch (reviewer + planner can run many minutes).
const HeartbeatTTL = 30 * time.Minute

// InFlightTTL is a backstop for in_flight aging when the transitioning pid is
// still alive (or unknown). The primary self-heal for a dead mid-transition
// process is InFlightPid + pidAlive (sty_bf797fa9 AC3) — not this TTL. Must
// exceed a long multi-gate dispatch (same order as HeartbeatTTL).
const InFlightTTL = HeartbeatTTL

// ErrNotFound is returned when Get misses.
var ErrNotFound = errors.New("lease: not found")

// ErrNotOwner is returned when Release is attempted by a non-owner.
var ErrNotOwner = errors.New("lease: not owner")

const schema = `
CREATE TABLE IF NOT EXISTS engagement_lease (
    item_id            TEXT PRIMARY KEY,
    kind               TEXT NOT NULL DEFAULT '',
    story_seat         INTEGER NOT NULL DEFAULT 0,
    owner              TEXT NOT NULL,
    state              TEXT NOT NULL DEFAULT '',
    acquired_at        TEXT NOT NULL,
    heartbeat_at       TEXT NOT NULL,
    stop_requested_by  TEXT NOT NULL DEFAULT '',
    stop_reason        TEXT NOT NULL DEFAULT '',
    in_flight          INTEGER NOT NULL DEFAULT 0,
    in_flight_at       TEXT NOT NULL DEFAULT '',
    in_flight_pid      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_engagement_lease_seat ON engagement_lease(story_seat);
`

// Lease is one engagement seat row.
type Lease struct {
	ItemID          string
	Kind            string
	StorySeat       bool
	Owner           string
	State           string
	AcquiredAt      time.Time
	HeartbeatAt     time.Time
	StopRequestedBy string
	StopReason      string
	InFlight        bool // transition in progress (gate+dispatch not yet committed)
	// InFlightAt is when in_flight was last set to 1. Zero when not in flight
	// or on pre-migration rows. Used by EffectiveInFlight (not HeartbeatAt —
	// hooks refresh heartbeat while the seat is held).
	InFlightAt time.Time
	// InFlightPid is the OS pid of the process that marked in_flight (the
	// transitioning CLI). 0 when unknown / not in flight. Distinct from Owner
	// (which is deliberately pid-less local@host so sequential CLI invocations
	// share the seat). When non-zero and dead on this host, EffectiveInFlight
	// is false immediately — the process that owned the transition is gone.
	InFlightPid int
}

// Outcome of Acquire.
type Outcome int

const (
	// OutcomeAcquired — this call inserted a new lease.
	OutcomeAcquired Outcome = iota
	// OutcomeAlreadyHeld — same owner holds a settled lease; sequential step may continue.
	OutcomeAlreadyHeld
	// OutcomeInFlight — same owner already has a transition in flight (AC3 no-op).
	OutcomeInFlight
	// OutcomeStolen — a stale holder was replaced by this call.
	OutcomeStolen
	// OutcomeConflict — another live owner holds this item or the story seat.
	OutcomeConflict
)

// Store wraps engagement_lease operations against a shared sqlite handle.
type Store struct{ db *sql.DB }

// New returns a Store bound to db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Migrate creates the engagement_lease table. Idempotent.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("lease: migrate: %w", err)
	}
	// Idempotent column add for DBs created before in_flight existed.
	if _, err := db.Exec(`ALTER TABLE engagement_lease ADD COLUMN in_flight INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("lease: migrate in_flight: %w", err)
	}
	// When in_flight was set (sty_bf797fa9). Empty string when not in flight.
	if _, err := db.Exec(`ALTER TABLE engagement_lease ADD COLUMN in_flight_at TEXT NOT NULL DEFAULT ''`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("lease: migrate in_flight_at: %w", err)
	}
	// Pid of the process that marked in_flight (sty_bf797fa9 AC3). 0 = unknown.
	if _, err := db.Exec(`ALTER TABLE engagement_lease ADD COLUMN in_flight_pid INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("lease: migrate in_flight_pid: %w", err)
	}
	// Backfill so pre-upgrade stuck in_flight rows age from a real clock, not
	// from a hook-refreshed heartbeat. Without this, zero-InFlightAt residue
	// cannot self-heal via the TTL backstop.
	if _, err := db.Exec(`UPDATE engagement_lease SET in_flight_at = heartbeat_at
		WHERE in_flight = 1 AND (in_flight_at = '' OR in_flight_at IS NULL)`); err != nil {
		return fmt.Errorf("lease: backfill in_flight_at: %w", err)
	}
	return nil
}

// EffectiveInFlight reports whether the lease is mid-transition for gate
// purposes. Consumers of "is a transition running right now" must use this
// rather than raw InFlight (architecture: domain calculation lives in lease).
//
// Order of checks:
//  1. raw InFlight false → not live
//  2. InFlightPid > 0 and dead on this host → not live (primary AC3 self-heal:
//     the process that owned the transition is gone; Owner stays pid-less)
//  3. zero InFlightAt → not live (residue after upgrade without backfill)
//  4. InFlightAt older than InFlightTTL → not live (backstop when pid unknown
//     or still alive but stuck)
func EffectiveInFlight(l Lease, now time.Time) bool {
	if !l.InFlight {
		return false
	}
	if l.InFlightPid > 0 && !pidAlive(l.InFlightPid) {
		return false
	}
	if l.InFlightAt.IsZero() {
		return false
	}
	return now.Sub(l.InFlightAt) <= InFlightTTL
}

// currentInFlightPid is the OS pid stamped onto a new in_flight mark.
// Overridable in tests.
var currentInFlightPid = func() int { return os.Getpid() }

// ResolveOwner returns the process owner identity for acquire/release.
// SATELLE_OWNER (trimmed) wins for multi-agent isolation. The default is
// local@hostname (no pid) so sequential CLI invocations on one machine share
// ownership — a pid-scoped default would break park/done release after the
// engage process exits (sty_8426b9c0). Process death is detected via
// HeartbeatTTL (and optional local@host:pid forms if an operator sets that).
func ResolveOwner() string {
	if v := strings.TrimSpace(os.Getenv("SATELLE_OWNER")); v != "" {
		return v
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return "local@" + host
}

// Acquire claims the seat for itemID. occupiesStorySeat gates the single-story
// rule (true for stories — one performing story seat). Returns the resulting
// lease, the outcome, and — on OutcomeConflict — the conflicting holder.
// check-and-insert runs in ONE immediate transaction.
func (s *Store) Acquire(ctx context.Context, itemID, kind, owner, state string, occupiesStorySeat bool) (Lease, Outcome, *Lease, error) {
	if s == nil || s.db == nil {
		return Lease{}, 0, nil, errors.New("lease: store not configured")
	}
	if strings.TrimSpace(itemID) == "" || strings.TrimSpace(owner) == "" {
		return Lease{}, 0, nil, fmt.Errorf("lease: item_id and owner required")
	}
	now := time.Now().UTC()
	nowS := now.Format(time.RFC3339Nano)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Lease{}, 0, nil, fmt.Errorf("lease: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Existing row for this item?
	cur, err := scanLease(tx.QueryRowContext(ctx,
		`SELECT item_id, kind, story_seat, owner, state, acquired_at, heartbeat_at, stop_requested_by, stop_reason, in_flight, in_flight_at, in_flight_pid
		 FROM engagement_lease WHERE item_id = ?`, itemID))
	if err == nil {
		if cur.Owner == owner {
			// Transition already in flight: AC3 no-op (never two concurrent dispatches).
			// Stuck flag self-heal: dead transitioning pid or aged in_flight_at →
			// treat as settled so the same owner can re-enter (sty_bf797fa9).
			if cur.InFlight && EffectiveInFlight(cur, now) {
				if _, err := tx.ExecContext(ctx,
					`UPDATE engagement_lease SET heartbeat_at = ? WHERE item_id = ?`, nowS, itemID); err != nil {
					return Lease{}, 0, nil, fmt.Errorf("lease: heartbeat: %w", err)
				}
				if err := tx.Commit(); err != nil {
					return Lease{}, 0, nil, err
				}
				cur.HeartbeatAt = now
				return cur, OutcomeInFlight, nil, nil
			}
			// Settled lease (or expired stuck in_flight), same owner: mark in_flight
			// for the new step. Do NOT advance state yet — state tracks last
			// COMMITTED engaging status; Confirm advances it after store.Update
			// (avoids gate-reject poison).
			pid := currentInFlightPid()
			if _, err := tx.ExecContext(ctx,
				`UPDATE engagement_lease SET in_flight = 1, in_flight_at = ?, in_flight_pid = ?, heartbeat_at = ?, kind = ?, story_seat = ?
				 WHERE item_id = ?`, nowS, pid, nowS, kind, boolInt(occupiesStorySeat), itemID); err != nil {
				return Lease{}, 0, nil, fmt.Errorf("lease: mark in_flight: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return Lease{}, 0, nil, err
			}
			cur.InFlight = true
			cur.InFlightAt = now
			cur.InFlightPid = pid
			cur.HeartbeatAt = now
			cur.Kind = kind
			cur.StorySeat = occupiesStorySeat
			return cur, OutcomeAlreadyHeld, nil, nil
		}
		// Different owner: stale → steal; live → conflict.
		if !isStale(cur, now) {
			h := cur
			return Lease{}, OutcomeConflict, &h, nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM engagement_lease WHERE item_id = ?`, itemID); err != nil {
			return Lease{}, 0, nil, fmt.Errorf("lease: steal delete: %w", err)
		}
		// fall through to insert; mark stolen
		stolen := cur
		l, err := insertLease(ctx, tx, itemID, kind, owner, state, occupiesStorySeat, nowS)
		if err != nil {
			return Lease{}, 0, nil, err
		}
		if err := tx.Commit(); err != nil {
			return Lease{}, 0, nil, err
		}
		return l, OutcomeStolen, &stolen, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Lease{}, 0, nil, err
	}

	// Story seat single-flight: another live story_seat=1 blocks.
	if occupiesStorySeat {
		holder, herr := scanLease(tx.QueryRowContext(ctx,
			`SELECT item_id, kind, story_seat, owner, state, acquired_at, heartbeat_at, stop_requested_by, stop_reason, in_flight, in_flight_at, in_flight_pid
			 FROM engagement_lease WHERE story_seat = 1 LIMIT 1`))
		if herr == nil {
			if holder.ItemID != itemID {
				if !isStale(holder, now) {
					h := holder
					return Lease{}, OutcomeConflict, &h, nil
				}
				// Steal stale seat holder.
				if _, err := tx.ExecContext(ctx, `DELETE FROM engagement_lease WHERE item_id = ?`, holder.ItemID); err != nil {
					return Lease{}, 0, nil, fmt.Errorf("lease: seat steal: %w", err)
				}
			}
		} else if !errors.Is(herr, ErrNotFound) {
			return Lease{}, 0, nil, herr
		}
	}

	l, err := insertLease(ctx, tx, itemID, kind, owner, state, occupiesStorySeat, nowS)
	if err != nil {
		return Lease{}, 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Lease{}, 0, nil, err
	}
	return l, OutcomeAcquired, nil, nil
}

func insertLease(ctx context.Context, tx *sql.Tx, itemID, kind, owner, state string, seat bool, nowS string) (Lease, error) {
	// New lease starts in_flight=1; Confirm clears it after status commits.
	pid := currentInFlightPid()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO engagement_lease (item_id, kind, story_seat, owner, state, acquired_at, heartbeat_at, stop_requested_by, stop_reason, in_flight, in_flight_at, in_flight_pid)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '', '', 1, ?, ?)`,
		itemID, kind, boolInt(seat), owner, state, nowS, nowS, nowS, pid); err != nil {
		return Lease{}, fmt.Errorf("lease: insert: %w", err)
	}
	now, _ := time.Parse(time.RFC3339Nano, nowS)
	return Lease{
		ItemID: itemID, Kind: kind, StorySeat: seat, Owner: owner, State: state,
		AcquiredAt: now, HeartbeatAt: now, InFlight: true, InFlightAt: now, InFlightPid: pid,
	}, nil
}

// Confirm settles an in-flight transition: records committed engaging state and
// clears in_flight. Called after store.Update succeeds for an engaging target.
func (s *Store) Confirm(ctx context.Context, itemID, committedState string) error {
	if s == nil || s.db == nil {
		return errors.New("lease: store not configured")
	}
	nowS := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`UPDATE engagement_lease SET state = ?, in_flight = 0, in_flight_at = '', in_flight_pid = 0, heartbeat_at = ? WHERE item_id = ?`,
		committedState, nowS, itemID)
	if err != nil {
		return fmt.Errorf("lease: confirm: %w", err)
	}
	return nil
}

// ClearInFlight drops the in_flight flag without changing state — used when a
// sequential step aborts (gate reject) so a retry can re-acquire the step.
func (s *Store) ClearInFlight(ctx context.Context, itemID string) error {
	if s == nil || s.db == nil {
		return errors.New("lease: store not configured")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE engagement_lease SET in_flight = 0, in_flight_at = '', in_flight_pid = 0 WHERE item_id = ?`, itemID)
	if err != nil {
		return fmt.Errorf("lease: clear in_flight: %w", err)
	}
	return nil
}

// Release frees the seat for itemID. Only the owner may release mid-flight
// (AC4/AC5). For workflow exit (terminal/park) use ForceRelease.
func (s *Store) Release(ctx context.Context, itemID, owner string) error {
	if s == nil || s.db == nil {
		return errors.New("lease: store not configured")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lease: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	cur, err := scanLease(tx.QueryRowContext(ctx,
		`SELECT item_id, kind, story_seat, owner, state, acquired_at, heartbeat_at, stop_requested_by, stop_reason, in_flight, in_flight_at, in_flight_pid
		 FROM engagement_lease WHERE item_id = ?`, itemID))
	if errors.Is(err, ErrNotFound) {
		return tx.Commit() // already free
	}
	if err != nil {
		return err
	}
	if cur.Owner != owner {
		return fmt.Errorf("%w: lease for %s held by %q (requester %q)", ErrNotOwner, itemID, cur.Owner, owner)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM engagement_lease WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("lease: delete: %w", err)
	}
	return tx.Commit()
}

// ForceRelease deletes the lease for itemID regardless of owner. Used when a
// workflow transition into terminal/park commits — the DOT exit is the
// authority that the seat is free (sequential CLI pids must not leave a stuck
// seat because the engage process already exited).
func (s *Store) ForceRelease(ctx context.Context, itemID string) error {
	if s == nil || s.db == nil {
		return errors.New("lease: store not configured")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM engagement_lease WHERE item_id = ?`, itemID)
	if err != nil {
		return fmt.Errorf("lease: force release: %w", err)
	}
	return nil
}

// RequestStop annotates the holder row with a stop request. Never deletes.
// Any principal may request; arbitration happens at the next step edge (AC5).
func (s *Store) RequestStop(ctx context.Context, itemID, requester, reason string) error {
	if s == nil || s.db == nil {
		return errors.New("lease: store not configured")
	}
	if strings.TrimSpace(requester) == "" {
		return fmt.Errorf("lease: requester required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE engagement_lease SET stop_requested_by = ?, stop_reason = ? WHERE item_id = ?`,
		requester, reason, itemID)
	if err != nil {
		return fmt.Errorf("lease: request stop: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("lease: no active lease for %s — nothing to stop", itemID)
	}
	return nil
}

// Get returns the lease for itemID or ErrNotFound.
func (s *Store) Get(ctx context.Context, itemID string) (Lease, error) {
	if s == nil || s.db == nil {
		return Lease{}, errors.New("lease: store not configured")
	}
	return scanLease(s.db.QueryRowContext(ctx,
		`SELECT item_id, kind, story_seat, owner, state, acquired_at, heartbeat_at, stop_requested_by, stop_reason, in_flight, in_flight_at, in_flight_pid
		 FROM engagement_lease WHERE item_id = ?`, itemID))
}

// AnyActive reports whether any engagement lease row exists. Callers that gate
// edit/commit must prefer List + a performing-state predicate (see evaluateSeat
// in the hook package): a bare row is not necessarily live engagement
// (sty_1738f973 — orphaned / stale / non-performing rows must not open the gate).
func (s *Store) AnyActive(ctx context.Context) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("lease: store not configured")
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM engagement_lease`).Scan(&n); err != nil {
		return false, fmt.Errorf("lease: any: %w", err)
	}
	return n > 0, nil
}

// List returns every engagement lease row (including stale / in-flight). The
// lease package is pure mechanism — callers decide which rows count as live
// engagement (staleness via IsStale; performing-state via workflow shape).
func (s *Store) List(ctx context.Context) ([]Lease, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("lease: store not configured")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT item_id, kind, story_seat, owner, state, acquired_at, heartbeat_at, stop_requested_by, stop_reason, in_flight, in_flight_at, in_flight_pid
		 FROM engagement_lease ORDER BY acquired_at`)
	if err != nil {
		return nil, fmt.Errorf("lease: list: %w", err)
	}
	defer rows.Close()
	var out []Lease
	for rows.Next() {
		var l Lease
		var seat, inflight, inflightPid int
		var acq, beat, inflightAt string
		if err := rows.Scan(&l.ItemID, &l.Kind, &seat, &l.Owner, &l.State, &acq, &beat, &l.StopRequestedBy, &l.StopReason, &inflight, &inflightAt, &inflightPid); err != nil {
			return nil, fmt.Errorf("lease: list scan: %w", err)
		}
		l.StorySeat = seat != 0
		l.InFlight = inflight != 0
		l.InFlightPid = inflightPid
		l.AcquiredAt, _ = time.Parse(time.RFC3339Nano, acq)
		if l.AcquiredAt.IsZero() {
			l.AcquiredAt, _ = time.Parse(time.RFC3339, acq)
		}
		l.HeartbeatAt, _ = time.Parse(time.RFC3339Nano, beat)
		if l.HeartbeatAt.IsZero() {
			l.HeartbeatAt, _ = time.Parse(time.RFC3339, beat)
		}
		l.InFlightAt = parseLeaseTime(inflightAt)
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lease: list: %w", err)
	}
	return out, nil
}

// IsStale reports whether a lease may be stolen or ignored for engagement: the
// heartbeat is older than HeartbeatTTL, or the owner embeds a dead local pid.
// Exported so gate/seat surfaces share one definition with Acquire steal paths.
func IsStale(l Lease, now time.Time) bool { return isStale(l, now) }

// Reap deletes rows whose heartbeat/pid marks them stale. Returns the reaped
// leases. Opportunistic housekeeping for seat-list honesty; gate correctness
// does not depend on Reap — IsStale already excludes those rows from engagement.
func (s *Store) Reap(ctx context.Context) ([]Lease, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("lease: store not configured")
	}
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var reaped []Lease
	for _, l := range all {
		if !IsStale(l, now) {
			continue
		}
		if err := s.ForceRelease(ctx, l.ItemID); err != nil {
			return reaped, err
		}
		reaped = append(reaped, l)
	}
	return reaped, nil
}

// Heartbeat refreshes heartbeat_at for the owner's lease (optional long-dispatch).
func (s *Store) Heartbeat(ctx context.Context, itemID, owner string) error {
	if s == nil || s.db == nil {
		return errors.New("lease: store not configured")
	}
	nowS := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE engagement_lease SET heartbeat_at = ? WHERE item_id = ? AND owner = ?`,
		nowS, itemID, owner)
	if err != nil {
		return fmt.Errorf("lease: heartbeat: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHeartbeat overwrites heartbeat_at for itemID. Used by regression tests and
// seat diagnostics to simulate a frozen heartbeat after process death
// (sty_1738f973 AC5 kill-simulation). Not an operator happy-path verb.
func (s *Store) SetHeartbeat(ctx context.Context, itemID string, at time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("lease: store not configured")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE engagement_lease SET heartbeat_at = ? WHERE item_id = ?`,
		at.UTC().Format(time.RFC3339Nano), itemID)
	if err != nil {
		return fmt.Errorf("lease: set heartbeat: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetInFlightAt overwrites in_flight_at for itemID (and forces in_flight=1).
// Test/diagnostic seam for stuck-flag aging (sty_bf797fa9). Not an operator path.
func (s *Store) SetInFlightAt(ctx context.Context, itemID string, at time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("lease: store not configured")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE engagement_lease SET in_flight = 1, in_flight_at = ? WHERE item_id = ?`,
		at.UTC().Format(time.RFC3339Nano), itemID)
	if err != nil {
		return fmt.Errorf("lease: set in_flight_at: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanLease(row *sql.Row) (Lease, error) {
	var l Lease
	var seat, inflight, inflightPid int
	var acq, beat, inflightAt string
	err := row.Scan(&l.ItemID, &l.Kind, &seat, &l.Owner, &l.State, &acq, &beat, &l.StopRequestedBy, &l.StopReason, &inflight, &inflightAt, &inflightPid)
	if errors.Is(err, sql.ErrNoRows) {
		return Lease{}, ErrNotFound
	}
	if err != nil {
		return Lease{}, fmt.Errorf("lease: scan: %w", err)
	}
	l.StorySeat = seat != 0
	l.InFlight = inflight != 0
	l.InFlightPid = inflightPid
	l.AcquiredAt, _ = time.Parse(time.RFC3339Nano, acq)
	if l.AcquiredAt.IsZero() {
		l.AcquiredAt, _ = time.Parse(time.RFC3339, acq)
	}
	l.HeartbeatAt, _ = time.Parse(time.RFC3339Nano, beat)
	if l.HeartbeatAt.IsZero() {
		l.HeartbeatAt, _ = time.Parse(time.RFC3339, beat)
	}
	l.InFlightAt = parseLeaseTime(inflightAt)
	return l, nil
}

func parseLeaseTime(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isStale reports whether a lease may be stolen: heartbeat older than TTL, or
// same-host embedded pid that is no longer alive.
func isStale(l Lease, now time.Time) bool {
	if !l.HeartbeatAt.IsZero() && now.Sub(l.HeartbeatAt) > HeartbeatTTL {
		return true
	}
	if host, pid, ok := parseLocalOwner(l.Owner); ok {
		myHost, _ := os.Hostname()
		if myHost != "" && host == myHost && !pidAlive(pid) {
			return true
		}
	}
	return false
}

// parseLocalOwner parses "local@hostname:pid".
func parseLocalOwner(owner string) (host string, pid int, ok bool) {
	if !strings.HasPrefix(owner, "local@") {
		return "", 0, false
	}
	rest := strings.TrimPrefix(owner, "local@")
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", 0, false
	}
	host = rest[:i]
	p, err := strconv.Atoi(rest[i+1:])
	if err != nil || p <= 0 {
		return "", 0, false
	}
	return host, p, true
}

// pidAlive reports whether pid is a live process on this host (best-effort).
// Overridable in tests via PidAlive.
var PidAlive = defaultPidAlive

func pidAlive(pid int) bool { return PidAlive(pid) }

func defaultPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Linux /proc is the reliable existence check; FindProcess always succeeds
	// on Unix and does not prove the process is alive.
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		return true
	}
	// Non-Linux: treat as alive so we never steal a live lease via false death.
	// HeartbeatTTL is the cross-platform stale path.
	if _, err := os.Stat("/proc"); errors.Is(err, os.ErrNotExist) {
		return true
	}
	return false
}
