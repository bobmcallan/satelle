package costview

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	// AllocatedRows counts the bundled-session shares folded into this row's
	// dollars and tokens (sty_23e10d92). A bundled reviewer session is ONE
	// measured invocation judging several rubrics; its usage is divided evenly
	// across those rubrics so each skill row has a figure, and that figure is an
	// ALLOCATION, never a second measured call. Invocations counts measured
	// calls only, so an allocated share never inflates it. AllocationNote labels
	// the allocation; AllocatedBundles names the bundles it came from;
	// AllocationUnavailableReason is the adapter-named reason when a bundle's
	// usage or cost was not reported, so those shares read as unavailable, never 0.
	AllocatedRows               int      `json:"allocated_rows,omitempty"`
	AllocatedBundles            []string `json:"allocated_bundles,omitempty"`
	AllocationNote              string   `json:"allocation_note,omitempty"`
	AllocationUnavailableReason string   `json:"allocation_unavailable_reason,omitempty"`
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
				seat := seatOf(row.Agent, modelOf(row.Model, row.ModelResolved))
				if n := len(row.BundleSkills); n > 1 {
					// One measured bundled call, divided across the rubrics it
					// judged — an allocation, labelled as one (sty_23e10d92).
					for i, sk := range row.BundleSkills {
						r := get(normalizeSkill(sk), seat)
						r.AllocatedRows++
						r.noteBundle(row)
						r.addUsage(bundleShare(row, i, n))
					}
					continue
				}
				r := get(normalizeSkill(row.Skill), seat)
				r.Invocations++
				r.addUsage(row)
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
		r.AllocationNote = allocationNote(r.AllocatedBundles)
		if r.Rejects > 0 && r.Costed > 0 {
			per := r.CostUSD / float64(r.Rejects)
			r.CostPerRejectUSD = &per
		}
		report.Rows = append(report.Rows, r)
	}
	return report
}

// addUsage folds one invocation row's dollars and tokens into r: a priced row
// into the known-dollar subtotal, an unpriced one into Uncosted, and usage
// split into fresh versus legacy-unsplit tokens. Shared by a measured row and a
// bundled share so the two can never account differently.
func (r *GateValueRow) addUsage(row Row) {
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
}

// noteBundle records that r carries an allocated share of row's bundle, and —
// when the bundle's usage or cost was not reported — the adapter's reason, so
// an unavailable share is explained rather than silently zero.
func (r *GateValueRow) noteBundle(row Row) {
	seen := false
	for _, id := range r.AllocatedBundles {
		seen = seen || id == row.BundleID
	}
	if !seen {
		r.AllocatedBundles = append(r.AllocatedBundles, row.BundleID)
	}
	if r.AllocationUnavailableReason != "" {
		return
	}
	switch {
	case !row.UsageAvailable && row.UsageUnavailableReason != "":
		r.AllocationUnavailableReason = row.UsageUnavailableReason
	case row.CostUSD == nil && row.CostUnavailableReason != "":
		r.AllocationUnavailableReason = row.CostUnavailableReason
	}
}

// allocationNote labels a row that carries bundled-session shares — empty when
// it carries none.
func allocationNote(bundles []string) string {
	switch len(bundles) {
	case 0:
		return ""
	case 1:
		return "allocated share of bundle " + bundles[0]
	default:
		return fmt.Sprintf("allocated share of %d bundles (%s)", len(bundles), strings.Join(bundles, ", "))
	}
}

// bundleShare is rubric i's (of n) share of a bundled row's measured usage: an
// even split whose remainder goes to the earliest shares, so the shares of one
// row sum EXACTLY to the measured figure. Cost gives the last share whatever the
// earlier ones left, for the same reason. Unavailable usage or cost stays
// unavailable in every share.
func bundleShare(row Row, i, n int) Row {
	s := row
	s.TokensIn = splitInt(row.TokensIn, i, n)
	s.TokensOut = splitInt(row.TokensOut, i, n)
	s.TokensTotal = splitInt(row.TokensTotal, i, n)
	s.TokensInFresh = splitInt(row.TokensInFresh, i, n)
	s.TokensCacheWrite = splitInt(row.TokensCacheWrite, i, n)
	s.TokensCacheRead = splitInt(row.TokensCacheRead, i, n)
	s.DurationMs = int64(splitInt(int(row.DurationMs), i, n))
	if row.CostUSD != nil {
		each := *row.CostUSD / float64(n)
		v := each
		if i == n-1 {
			v = *row.CostUSD - each*float64(n-1)
		}
		s.CostUSD = &v
	}
	return s
}

// splitInt is the i'th of n near-equal integer parts of total; the parts sum to
// total.
func splitInt(total, i, n int) int {
	q := total / n
	if i < total%n {
		q++
	}
	return q
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
