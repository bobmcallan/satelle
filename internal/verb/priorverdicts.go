package verb

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// PriorVerdict is one verdict already recorded for an item on one from→to edge.
// Decision is "accept" or "reject"; CreatedAt is RFC3339.
type PriorVerdict struct {
	Skill             string `json:"skill,omitempty"`
	Decision          string `json:"decision"`
	Notes             string `json:"notes,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	Reviewed          string `json:"reviewed,omitempty"`
	ReviewedTruncated bool   `json:"reviewed_truncated,omitempty"`
	// ReviewedStale marks a skill's latest verdict whose review cycle ended
	// before new evidence was recorded on the story. Reviewed is then cleared —
	// the rubric's "no reviewed string, re-judge" rule fires — and
	// EvidenceSince lists what was recorded since (sty_0225fc2f).
	ReviewedStale bool            `json:"reviewed_stale,omitempty"`
	EvidenceSince []EvidenceSince `json:"evidence_since,omitempty"`
}

// EvidenceSince is one ledger row recorded after a verdict's review cycle
// ended. Event is the telemetry envelope's kind when the row is a telemetry
// event (e.g. plan-consumed), else empty.
type EvidenceSince struct {
	Kind      string `json:"kind"`
	Event     string `json:"event,omitempty"`
	CreatedAt string `json:"created_at"`
}

// maxEvidenceSince bounds the evidence list a stale verdict carries.
const maxEvidenceSince = 20

// isSelfLedgerKind reports whether kind is a ledger kind the binary writes about
// its own review or dispatch bookkeeping. These are never evidence a rubric could
// be waiting on, so they never mark a verdict stale — even when written after
// the cycle ended.
func isSelfLedgerKind(kind string) bool {
	switch kind {
	case ledger.KindAgentInvocation, ledger.KindStepCost, ledger.KindDriverUsage,
		ledger.KindSessionModel, ledger.KindToolPermission, ledger.KindSessionAdvisory,
		ledger.KindBudgetOverrun, ledger.KindGateSkipped,
		ledger.KindReviewAccept, ledger.KindReviewReject:
		return true
	}
	return false
}

// isSelfTelemetryKind reports whether a telemetry envelope kind is one the
// dispatch engine writes for itself. Driver-recorded telemetry (story log) is
// not among them.
func isSelfTelemetryKind(kind string) bool {
	switch kind {
	case "agent-attempt", "gate-bundled", "agent-retry", "agent-failure",
		"agent-timeout", "agent-stalled", "scoped-gate-skipped",
		"reviewer-isolation-warned", "agent-secondary-failover", "agent-interactive-denied":
		return true
	}
	return false
}

// priorVerdictRow is the reviewer row's payload as reviewerPayload
// (workitem.go) writes it — the decision itself comes from the row's KIND,
// which is what the trail is indexed by.
type priorVerdictRow struct {
	From              string `json:"from"`
	To                string `json:"to"`
	Skill             string `json:"skill,omitempty"`
	Notes             string `json:"notes,omitempty"`
	Reviewed          string `json:"reviewed,omitempty"`
	ReviewedTruncated bool   `json:"reviewed_truncated,omitempty"`
	// RecordedAt is the wall-clock time the verdict row was appended, after the
	// gate run that produced it finished. Rows written before the field existed
	// carry none and are never marked stale.
	RecordedAt string `json:"recorded_at,omitempty"`
}

// PriorVerdicts returns every review verdict already recorded for itemID on the
// from→to edge, oldest first — the enumeration a re-reviewing gate needs to
// judge the delta rather than re-deriving a verdict from scratch
// (sty_0f5e600c). Rows for other edges of the same story are excluded. The
// ledger already carries the edge in each reviewer row's payload, so this is a
// read: no schema change, and history written before the feature works
// retroactively.
//
// Each skill's LATEST verdict is checked for staleness: when a story ledger row
// that is not a binary self-report was recorded after that verdict's cycle ended
// (its recorded_at), the verdict loses its Reviewed quotation and carries
// ReviewedStale and EvidenceSince instead. The gate then re-judges rather than
// re-issuing a verdict whose material it could not see changing. The binary only
// reports that evidence arrived; whether it answers the objection stays the
// rubric's call.
//
// Nil-safe when no ledger is wired, and never an error the caller must handle
// specially: prior verdicts are additive context, so an unreadable row is
// skipped rather than failing the transition it decorates.
func PriorVerdicts(ctx context.Context, itemID, from, to string) ([]PriorVerdict, error) {
	if ledgerStore == nil || strings.TrimSpace(itemID) == "" {
		return nil, nil
	}
	entries, err := ledgerStore.ListByStory(ctx, itemID, "")
	if err != nil {
		return nil, err
	}
	return PriorVerdictsFrom(entries, from, to), nil
}

// PriorVerdictsFrom is the enumeration PriorVerdicts runs over a story's ledger
// rows, oldest first. It is exported so a replay can rebuild what a gate injected
// from rows alone.
func PriorVerdictsFrom(entries []ledger.Entry, from, to string) []PriorVerdict {
	var out []PriorVerdict
	var recorded []time.Time // parallel to out; zero when the row has no recorded_at
	for _, e := range entries {
		var decision string
		switch e.Kind {
		case ledger.KindReviewAccept:
			decision = "accept"
		case ledger.KindReviewReject:
			decision = "reject"
		default:
			continue
		}
		var row priorVerdictRow
		if err := json.Unmarshal(e.Payload, &row); err != nil {
			continue
		}
		if row.From != from || row.To != to {
			continue
		}
		out = append(out, PriorVerdict{
			Skill:             row.Skill,
			Decision:          decision,
			Notes:             row.Notes,
			CreatedAt:         e.CreatedAt.UTC().Format(time.RFC3339),
			Reviewed:          row.Reviewed,
			ReviewedTruncated: row.ReviewedTruncated,
		})
		var at time.Time
		if row.RecordedAt != "" {
			at, _ = time.Parse(time.RFC3339Nano, row.RecordedAt)
		}
		recorded = append(recorded, at)
	}
	markStale(out, recorded, entries)
	return out
}

// markStale applies the staleness rule to each skill's latest verdict in out.
func markStale(out []PriorVerdict, recorded []time.Time, entries []ledger.Entry) {
	latest := make(map[string]int, len(out))
	for i, v := range out {
		latest[v.Skill] = i
	}
	for _, i := range latest {
		if recorded[i].IsZero() {
			continue
		}
		since := evidenceAfter(entries, recorded[i])
		if len(since) == 0 {
			continue
		}
		out[i].Reviewed, out[i].ReviewedTruncated = "", false
		out[i].ReviewedStale, out[i].EvidenceSince = true, since
	}
}

// evidenceAfter lists the rows recorded strictly after cycleEnd that are not
// binary self-reports, oldest first, capped at maxEvidenceSince.
func evidenceAfter(entries []ledger.Entry, cycleEnd time.Time) []EvidenceSince {
	var since []EvidenceSince
	for _, e := range entries {
		if !e.CreatedAt.After(cycleEnd) || isSelfLedgerKind(e.Kind) {
			continue
		}
		var event string
		if e.Kind == ledger.KindTelemetryEvent {
			var env telemetryEnvelope
			if json.Unmarshal(e.Payload, &env) == nil {
				event = env.Kind
			}
			if isSelfTelemetryKind(event) {
				continue
			}
		}
		since = append(since, EvidenceSince{Kind: e.Kind, Event: event, CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339Nano)})
		if len(since) == maxEvidenceSince {
			break
		}
	}
	return since
}
