//go:build plannerbench

package plannerbench

import (
	"regexp"
	"strconv"
)

// usageEvidence is attempt-AGGREGATED token usage. The schema-1 version read
// the FIRST regex match in the ledger text, so it reported one attempt's tokens
// as the run's cost even though the repair/escalate policy now makes up to three;
// and it turned an unreported total into an available zero. Both are fixed here:
// tokens are summed across every attempt event, and the numeric fields are
// POINTERS that stay omitted unless some attempt actually reported usage.
type usageEvidence struct {
	Available         bool   `json:"available"`
	TokensIn          *int   `json:"tokens_in,omitempty"`
	TokensOut         *int   `json:"tokens_out,omitempty"`
	TokensTotal       *int   `json:"tokens_total,omitempty"`
	DurationMS        int64  `json:"attempt_duration_ms"`
	AttemptsTotal     int    `json:"attempts_total"`
	AttemptsWithUsage int    `json:"attempts_with_usage"`
	Provenance        string `json:"provenance"`
	// CostTotal is the `satelle story cost` cross-check. It is recorded beside
	// the aggregate, never as a substitute for it, and it can NEVER flip
	// Available: a "TOTAL 0" line is not evidence that usage was reported.
	CostTotal      *int   `json:"cost_total,omitempty"`
	CostProvenance string `json:"cost_provenance,omitempty"`
}

// totalCostRE reads the `satelle story cost` TOTAL row. It feeds the
// cross-check field only.
var totalCostRE = regexp.MustCompile(`(?m)^TOTAL\s+(\d+)\s+`)

// aggregateUsage sums usage across every attempt of a run.
//
// Availability rule: available is true only when at least one attempt event
// reported usage. When none did, the token pointers stay nil and marshal away
// entirely, so an unreported run can never serialise as 0. A genuinely reported
// zero stays available with tokens_total 0 — reported-zero and unreported are
// different facts and the record keeps them different.
func aggregateUsage(entries []ledgerEntry, cost string) usageEvidence {
	attempts := attemptEvents(entries)
	usage := usageEvidence{AttemptsTotal: len(attempts), Provenance: "ledger-agent-attempt-sum"}
	var in, out, total int
	for _, a := range attempts {
		usage.DurationMS += a.DurationMS
		if !a.UsageAvailable {
			continue
		}
		usage.AttemptsWithUsage++
		usage.Available = true
		if a.TokensIn != nil {
			in += *a.TokensIn
		}
		if a.TokensOut != nil {
			out += *a.TokensOut
		}
		if a.TokensTotal != nil {
			total += *a.TokensTotal
		}
	}
	if usage.Available {
		usage.TokensIn, usage.TokensOut, usage.TokensTotal = &in, &out, &total
	} else if len(attempts) == 0 {
		usage.Provenance = "no-attempt-events-recorded"
	} else {
		usage.Provenance = "transport-unreported"
	}
	if m := totalCostRE.FindStringSubmatch(cost); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			usage.CostTotal = &n
			usage.CostProvenance = "story-cost-total"
		}
	}
	return usage
}
