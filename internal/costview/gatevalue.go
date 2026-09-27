package costview

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// GateFilter narrows GateValue to a date range on entry timestamps. Which
// stories' entries are handed in at all (one story, or a whole epic family
// found by the Family walk) is the caller's concern — GateValue does no
// story/epic selection itself, only the date bound.
type GateFilter struct {
	Since time.Time
	Until time.Time
}

func (f GateFilter) includes(t time.Time) bool {
	if !f.Since.IsZero() && t.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && t.After(f.Until) {
		return false
	}
	return true
}

// GateValueRow is one skill/seat combination's aggregated spend against its
// verdicts. Seat is the recorded agent and model (e.g. "reviewer@opus") — the
// ledger's agent_invocation and review rows carry no harness field today, so
// seat cannot include one; a row with neither recorded groups as "unknown",
// never defaulting to a provider. CostUSD/Costed/Uncosted follow the same
// known-vs-unavailable split as Figures — an uncosted row is never folded in
// as a zero.
//
// FreshTokens sums only rows that carry the fresh/cache split; UnsplitTokens
// sums a legacy row's cache-inclusive TokensIn recorded before that split
// existed — folded into FreshTokens it would overstate fresh work with reused
// cache context, so it stays its own field (the same split Figures and
// SkillRollupRow use).
//
// UsageRows/UsageUnavailableRows count invocation rows that reported usage at
// all versus rows that did not — the same split Figures.UsageRows keeps —
// so a caller rendering FreshTokens can tell "measured zero" from "nothing
// measured" instead of a row whose provider reported no usage silently
// printing FreshTokens==0 as though it were a real figure.
//
// UnsplitRows counts, among UsageRows, how many reported only the legacy
// cache-inclusive total rather than the fresh/cache split (their tokens went
// to UnsplitTokens, not FreshTokens) — the same split Figures.UnsplitRows
// keeps. UsageRows-UnsplitRows is how many rows actually fed FreshTokens; a
// row/seat whose usage rows are ALL legacy-unsplit must render FreshTokens as
// "unavailable (N unsplit)", never a literal 0 (see FormatSplitTokens).
//
// CostPerRejectUSD is CostUSD/Rejects using the known-dollar subtotal — nil
// when Rejects is 0 (division by zero rejects is not a cost of zero) or when
// Costed is 0 (nothing priced to divide); a JSON caller reads this directly
// rather than re-deriving FormatCostPerReject's number by hand, so --json and
// the CLI table report the identical figure. CostUSD marshals as JSON null
// (via MarshalJSON below) when Costed is 0 — an uncosted row's total is never
// a fabricated $0.
type GateValueRow struct {
	Skill                string   `json:"skill"`
	Seat                 string   `json:"seat"`
	Invocations          int      `json:"invocations"`
	CostUSD              float64  `json:"cost_usd"`
	Costed               int      `json:"costed_rows"`
	Uncosted             int      `json:"uncosted_rows"`
	FreshTokens          int      `json:"fresh_tokens"`
	UnsplitTokens        int      `json:"unsplit_tokens,omitempty"`
	UsageRows            int      `json:"usage_rows"`
	UsageUnavailableRows int      `json:"usage_unavailable_rows"`
	UnsplitRows          int      `json:"unsplit_rows,omitempty"`
	Accepts              int      `json:"accepts"`
	Rejects              int      `json:"rejects"`
	CostPerRejectUSD     *float64 `json:"cost_per_reject_usd,omitempty"`
}

// MarshalJSON renders CostUSD as null when Costed is 0 — an uncosted row's
// sum is the empty sum, never a fabricated $0 that a JSON consumer could
// mistake for a priced zero (sty_b8542a3a AC6 rework).
func (r GateValueRow) MarshalJSON() ([]byte, error) {
	type alias GateValueRow
	out := struct {
		alias
		CostUSD *float64 `json:"cost_usd"`
	}{alias: alias(r)}
	if r.Costed > 0 {
		v := r.CostUSD
		out.CostUSD = &v
	}
	return json.Marshal(out)
}

// UnmarshalJSON is MarshalJSON's counterpart: a null/absent cost_usd decodes
// to CostUSD==0, consistent with Costed==0 meaning "nothing priced" rather
// than "priced at zero".
func (r *GateValueRow) UnmarshalJSON(data []byte) error {
	type alias GateValueRow
	aux := struct {
		*alias
		CostUSD *float64 `json:"cost_usd"`
	}{alias: (*alias)(r)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.CostUSD != nil {
		r.CostUSD = *aux.CostUSD
	} else {
		r.CostUSD = 0
	}
	return nil
}

// GateValueReport is the whole gate-value view: every skill/seat row, sorted
// by skill then seat for a deterministic report.
type GateValueReport struct {
	Rows []GateValueRow `json:"rows"`
}

// seatOf formats the recorded agent + model as one seat identity. Empty stays
// empty so the caller can fold it into "unknown" — never a Claude default
// (satelle-agent-agnostic).
func seatOf(agent, model string) string {
	if agent == "" && model == "" {
		return "unknown"
	}
	if model == "" {
		return agent
	}
	if agent == "" {
		return model
	}
	return agent + "@" + model
}

// GateValue aggregates every agent_invocation and review_accept/review_reject
// row across entriesByID into a GateValueReport: invocations, dollars, fresh
// tokens, accepts and rejects per skill/seat, filtered to filter's date range.
// It is a QUERY over stored evidence — mechanism, not a gate decision — and
// performs no I/O: entriesByID is supplied by the caller, already scoped to
// one story, a whole epic family, or a date-bounded repo-wide scan.
func GateValue(entriesByID map[string][]ledger.Entry, filter GateFilter) GateValueReport {
	rows := map[[2]string]*GateValueRow{}
	var order [][2]string
	get := func(skill, seat string) *GateValueRow {
		k := [2]string{skill, seat}
		r, ok := rows[k]
		if !ok {
			r = &GateValueRow{Skill: skill, Seat: seat}
			rows[k] = r
			order = append(order, k)
		}
		return r
	}

	for _, entries := range entriesByID {
		for _, e := range entries {
			if !filter.includes(e.CreatedAt) {
				continue
			}
			switch e.Kind {
			case ledger.KindAgentInvocation:
				row, ok := DecodeRow(e)
				if !ok {
					continue
				}
				r := get(normalizeSkill(row.Skill), seatOf(row.Agent, modelOf(row.Model, row.ModelResolved)))
				r.Invocations++
				if row.CostUSD != nil {
					r.CostUSD += *row.CostUSD
					r.Costed++
				} else {
					r.Uncosted++
				}
				if row.UsageAvailable {
					r.UsageRows++
					if row.TokensInFresh > 0 || row.TokensCacheWrite > 0 || row.TokensCacheRead > 0 {
						r.FreshTokens += row.TokensInFresh
					} else {
						// Recorded before the fresh/cache split existed: TokensIn is
						// cache-inclusive, so it is legacy UNSPLIT input, never fresh
						// (AC1's split applies here too — cache reuse is not fresh work).
						r.UnsplitTokens += row.TokensIn
						r.UnsplitRows++
					}
				} else {
					r.UsageUnavailableRows++
				}
			case ledger.KindReviewAccept, ledger.KindReviewReject:
				var v struct {
					Skill         string `json:"skill"`
					Agent         string `json:"agent"`
					Model         string `json:"model"`
					ModelResolved string `json:"model_resolved"`
					Accept        bool   `json:"accept"`
				}
				if len(e.Payload) == 0 || json.Unmarshal(e.Payload, &v) != nil {
					continue
				}
				agent := v.Agent
				if agent == "" {
					agent = "reviewer"
				}
				r := get(normalizeSkill(v.Skill), seatOf(agent, modelOf(v.Model, v.ModelResolved)))
				if e.Kind == ledger.KindReviewAccept {
					r.Accepts++
				} else {
					r.Rejects++
				}
			}
		}
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i][0] != order[j][0] {
			return order[i][0] < order[j][0]
		}
		return order[i][1] < order[j][1]
	})
	report := GateValueReport{}
	for _, k := range order {
		r := *rows[k]
		if r.Rejects > 0 && r.Costed > 0 {
			per := r.CostUSD / float64(r.Rejects)
			r.CostPerRejectUSD = &per
		}
		report.Rows = append(report.Rows, r)
	}
	return report
}

// normalizeSkill maps an empty skill to "unknown" — applied identically to an
// agent_invocation row and a review_accept/review_reject row so a gate with
// no named skill on EITHER side lands in the SAME row, never split into two
// (one carrying the spend, the other the verdict) that silently break the
// invocations-vs-verdicts correlation this view exists to report
// (sty_b8542a3a AC6 rework). Previously the invocation branch alone fell back
// to the row's Agent when Skill was empty — a fallback the verdict branch
// never had, which could separate a gate's cost from its verdicts.
func normalizeSkill(skill string) string {
	if skill == "" {
		return "unknown"
	}
	return skill
}

// modelOf prefers the resolved model id over the configured alias, so two
// bindings that resolve to the same model report as one seat.
func modelOf(model, resolved string) string {
	if resolved != "" {
		return resolved
	}
	return model
}
