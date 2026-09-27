package costview

import (
	"encoding/json"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Figures is one item's own computed cost — dollars, the fresh/output/cache
// split, and time — derived solely from its ledger rows. CostUSD sums only
// rows that carry cost_usd; CostUnavailableRows counts the rest — an unpriced
// row is never folded in as a zero. CacheWrite is its own field, never folded
// into FreshInput or CacheRead. UnsplitTokens carries a legacy row's TokensIn
// recorded before the fresh/cache split existed.
//
// ElapsedMs is the story clock: first entry into an engaging state to first
// entry into a terminal state (by shape — never a park), or to now when the
// item is still open. AgentMs is DispatchMs (agent_invocation durations) plus
// DriverMs (driver_usage wall time), each also kept separately.
type Figures struct {
	CostUSD             float64 `json:"cost_usd"`
	CostRows            int     `json:"cost_rows"`
	CostUnavailableRows int     `json:"cost_unavailable_rows"`

	FreshInput    int `json:"fresh_input"`
	Output        int `json:"output"`
	CacheRead     int `json:"cache_read"`
	CacheWrite    int `json:"cache_write"`
	UnsplitTokens int `json:"unsplit_tokens,omitempty"`

	// UsageRows/UsageUnavailableRows count rows that fed the token figures
	// above, kept deliberately separate from CostRows/CostUnavailableRows: a
	// row can be priced (CostRows++) while its provider reported no usage at
	// all (UsageAvailable/Available false, contributing no tokens), so cost
	// availability can never stand in for token availability.
	UsageRows            int `json:"usage_rows"`
	UsageUnavailableRows int `json:"usage_unavailable_rows"`

	// UnsplitRows counts measured rows (UsageAvailable true) that reported
	// only the legacy cache-inclusive total, not the fresh/cache split —
	// UsageRows-UnsplitRows is therefore how many measured rows actually fed
	// FreshInput/CacheRead/CacheWrite. A story whose usage rows are ALL
	// legacy-unsplit has UsageRows>0 but zero split-reporting rows, so those
	// three figures must never render a bare 0 — see FormatSplitTokens.
	UnsplitRows int `json:"unsplit_rows,omitempty"`

	ElapsedMs  int64 `json:"elapsed_ms"`
	AgentMs    int64 `json:"agent_ms"`
	DispatchMs int64 `json:"dispatch_ms"`
	DriverMs   int64 `json:"driver_ms"`
}

// HasRows reports whether any dispatch or driver row actually measured usage
// — UsageRows counts only rows whose provider reported token usage
// (UsageAvailable/Available true); a row that was priced but reported no
// usage does NOT count here, so a story whose rows are all cost-only-or-
// unreported renders "unavailable" for FreshInput/Output/CacheRead/
// CacheWrite rather than a literal 0, which would misrepresent "nothing
// measured" as "measured nothing".
func (f Figures) HasRows() bool {
	return f.UsageRows > 0
}

// Span is an item's own wall-clock window — engage to terminal (or now).
// Kept separate from Figures.ElapsedMs so a family of items can be unioned
// into the family's wall span rather than summed: a parent open the whole
// time its children run must not report their overlapping clocks added
// together.
type Span struct {
	Start time.Time
	End   time.Time
}

// UnionSpan folds two spans into the one that covers both — the earliest
// Start and the latest End. Either side may be nil (no span to contribute);
// nil is the identity element.
func UnionSpan(a, b *Span) *Span {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	start, end := a.Start, a.End
	if b.Start.Before(start) {
		start = b.Start
	}
	if b.End.After(end) {
		end = b.End
	}
	return &Span{Start: start, End: end}
}

// Fold sums two Figures — used to fold a child's (subtree) figures into a
// parent's running family total.
func Fold(a, b Figures) Figures {
	return Figures{
		CostUSD:              a.CostUSD + b.CostUSD,
		CostRows:             a.CostRows + b.CostRows,
		CostUnavailableRows:  a.CostUnavailableRows + b.CostUnavailableRows,
		FreshInput:           a.FreshInput + b.FreshInput,
		Output:               a.Output + b.Output,
		CacheRead:            a.CacheRead + b.CacheRead,
		CacheWrite:           a.CacheWrite + b.CacheWrite,
		UnsplitTokens:        a.UnsplitTokens + b.UnsplitTokens,
		UsageRows:            a.UsageRows + b.UsageRows,
		UsageUnavailableRows: a.UsageUnavailableRows + b.UsageUnavailableRows,
		UnsplitRows:          a.UnsplitRows + b.UnsplitRows,
		ElapsedMs:            a.ElapsedMs + b.ElapsedMs,
		AgentMs:              a.AgentMs + b.AgentMs,
		DispatchMs:           a.DispatchMs + b.DispatchMs,
		DriverMs:             a.DriverMs + b.DriverMs,
	}
}

// Row is one dispatched/reviewed step's recorded cost — the per-gate tokens +
// wall-time captured on an agent_invocation ledger entry. UsageAvailable is
// true only when the transport reported usage (or, for legacy rows without
// the field, when tokens_total > 0). False means unreported — never a
// measured zero.
type Row struct {
	From           string `json:"from"`
	To             string `json:"to"`
	Agent          string `json:"agent"`
	Skill          string `json:"skill,omitempty"`
	Model          string `json:"model,omitempty"`
	ModelResolved  string `json:"model_resolved,omitempty"`
	TokensIn       int    `json:"tokens_in"`
	TokensOut      int    `json:"tokens_out"`
	TokensTotal    int    `json:"tokens_total"`
	DurationMs     int64  `json:"duration_ms"`
	UsageAvailable bool   `json:"usage_available"`
	// UsageUnavailableReason names the adapter and why usage is unavailable;
	// CacheSplitUnavailable marks a provider that reported no cache fields, so
	// the zero split below is unreported rather than measured.
	UsageUnavailableReason string `json:"usage_unavailable_reason,omitempty"`
	CacheSplitUnavailable  bool   `json:"cache_split_unavailable,omitempty"`
	// TokensInFresh/TokensCacheWrite/TokensCacheRead split TokensIn into its
	// disjoint components. Zero on a row recorded before this split existed —
	// reported as "unsplit" rather than mistaken for a measured zero.
	TokensInFresh    int `json:"tokens_in_fresh,omitempty"`
	TokensCacheWrite int `json:"tokens_cache_write,omitempty"`
	TokensCacheRead  int `json:"tokens_cache_read,omitempty"`
	// CostUSD is the row's dollar cost; nil (never a measured zero) when the
	// provider reported none, with CostUnavailableReason naming the adapter
	// and why.
	CostUSD               *float64 `json:"cost_usd,omitempty"`
	CostUnavailableReason string   `json:"cost_unavailable_reason,omitempty"`
}

// DecodeRow decodes one agent_invocation ledger entry into a Row, or ok=false
// when e carries no payload, fails to decode, or is a tool-permission event
// masquerading as an invocation (ledger.IsToolPermissionRow) — the single
// decode point every caller (the story-actual clock, the cost view, the
// gate-value roll-up) shares so agent_invocation handling cannot drift apart.
func DecodeRow(e ledger.Entry) (Row, bool) {
	if len(e.Payload) == 0 || ledger.IsToolPermissionRow(e) {
		return Row{}, false
	}
	var meta struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Agent string `json:"agent"`
		Skill string `json:"skill"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(e.Payload, &meta); err != nil {
		return Row{}, false
	}
	tel := ledger.EventTelemetry(e)
	row := Row{
		From:                   meta.From,
		To:                     meta.To,
		Agent:                  meta.Agent,
		Skill:                  meta.Skill,
		Model:                  meta.Model,
		ModelResolved:          tel.ModelResolved,
		TokensIn:               tel.TokensIn,
		TokensOut:              tel.TokensOut,
		TokensTotal:            tel.TokensTotal,
		DurationMs:             tel.DurationMs,
		UsageAvailable:         tel.UsageAvailable,
		UsageUnavailableReason: tel.UsageUnavailableReason,
		CacheSplitUnavailable:  tel.CacheSplitUnavailable,
		TokensInFresh:          tel.TokensInFresh,
		TokensCacheWrite:       tel.TokensCacheWrite,
		TokensCacheRead:        tel.TokensCacheRead,
		CostUSD:                tel.CostUSD,
		CostUnavailableReason:  tel.CostUnavailableReason,
	}
	if row.Agent == "" {
		row.Agent = tel.Agent
	}
	if row.Model == "" {
		row.Model = tel.Model
	}
	return row, true
}

// DriverRow is the driving (in-loop) session's own recorded usage on one
// engage/close/park window (sty_81caa41b) — the figures subset costview needs
// to fold a driver_usage ledger entry into Figures. The richer payload (session
// cumulative reconciliation, late/pending markers) stays a verb concern; a
// driver_usage entry decodes cleanly into either shape since JSON decode
// ignores fields a target struct does not declare.
type DriverRow struct {
	SessionID  string `json:"session_id"`
	Executable string `json:"executable"`
	Model      string `json:"model,omitempty"`

	FreshInput int `json:"fresh_input"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
	Output     int `json:"output"`

	CostUSD               *float64 `json:"cost_usd,omitempty"`
	CostUnavailableReason string   `json:"cost_unavailable_reason,omitempty"`

	Available bool `json:"available"`

	WallSeconds float64 `json:"wall_seconds"`
	Trigger     string  `json:"trigger"`
	From        string  `json:"from,omitempty"`
	To          string  `json:"to,omitempty"`
}

// DecodeDriverRow decodes one driver_usage ledger entry into a DriverRow, or
// ok=false when e carries no payload or fails to decode.
func DecodeDriverRow(e ledger.Entry) (DriverRow, bool) {
	if len(e.Payload) == 0 {
		return DriverRow{}, false
	}
	var d DriverRow
	if err := json.Unmarshal(e.Payload, &d); err != nil {
		return DriverRow{}, false
	}
	return d, true
}

// accumulator folds ledger rows into Figures. Kept as its own type (rather
// than inline in Own) so a dollar/token/time field added to one accumulation
// point cannot silently miss the other.
type accumulator struct {
	costUSD             float64
	costRows            int
	costUnavailableRows int

	freshInput, output, cacheRead, cacheWrite, unsplit int

	usageRows, usageUnavailableRows, unsplitRows int

	dispatchMs, driverMs int64
}

// addDispatchRow folds one agent_invocation row (already tool-permission-
// filtered by DecodeRow). DurationMs and cost fold in regardless of token
// usage availability — an agent still spent wall time and may still have a
// priced cost even when its provider reported no token split. Token figures
// fold in only from a measured row — never a zero for an unreported one.
func (a *accumulator) addDispatchRow(row Row) {
	a.dispatchMs += row.DurationMs
	if row.CostUSD != nil {
		a.costUSD += *row.CostUSD
		a.costRows++
	} else {
		a.costUnavailableRows++
	}
	if !row.UsageAvailable {
		a.usageUnavailableRows++
		return
	}
	a.usageRows++
	if row.TokensInFresh > 0 || row.TokensCacheWrite > 0 || row.TokensCacheRead > 0 {
		a.freshInput += row.TokensInFresh
		a.cacheWrite += row.TokensCacheWrite
		a.cacheRead += row.TokensCacheRead
	} else {
		a.unsplit += row.TokensIn
		a.unsplitRows++
	}
	a.output += row.TokensOut
}

// addDriverRow folds one driver_usage row. WallSeconds is real elapsed time
// recorded regardless of whether the harness's usage read succeeded, so it
// always folds in; the token/cost fields only ever carry non-zero values on
// an Available row.
func (a *accumulator) addDriverRow(d DriverRow) {
	a.driverMs += int64(d.WallSeconds * 1000)
	if d.CostUSD != nil {
		a.costUSD += *d.CostUSD
		a.costRows++
	} else {
		a.costUnavailableRows++
	}
	if !d.Available {
		a.usageUnavailableRows++
		return
	}
	a.usageRows++
	a.freshInput += d.FreshInput
	a.output += d.Output
	a.cacheRead += d.CacheRead
	a.cacheWrite += d.CacheWrite
}

func (a *accumulator) figures() Figures {
	return Figures{
		CostUSD:              a.costUSD,
		CostRows:             a.costRows,
		CostUnavailableRows:  a.costUnavailableRows,
		FreshInput:           a.freshInput,
		Output:               a.output,
		CacheRead:            a.cacheRead,
		CacheWrite:           a.cacheWrite,
		UnsplitTokens:        a.unsplit,
		UsageRows:            a.usageRows,
		UsageUnavailableRows: a.usageUnavailableRows,
		UnsplitRows:          a.unsplitRows,
		AgentMs:              a.dispatchMs + a.driverMs,
		DispatchMs:           a.dispatchMs,
		DriverMs:             a.driverMs,
	}
}

// Story is one item's own computed cost view: its Figures, the dispatch Rows
// and DriverRows that fed them, and its own wall-clock Span. Span is nil when
// the item has never entered an engaging state (its clock has not started —
// see Own), so a family walk never folds a not-started item's window into the
// union as though it had one. Built by Own.
type Story struct {
	ID         string      `json:"id"`
	Figures    Figures     `json:"figures"`
	Rows       []Row       `json:"rows,omitempty"`
	DriverRows []DriverRow `json:"driver_rows,omitempty"`
	Span       *Span       `json:"-"`
	Estimates  []Estimate  `json:"estimates,omitempty"`
}

// Own reads item's ledger entries and returns its own Story — figures, rows,
// driver rows, span and parsed estimates — with no child rollup. The story
// clock is derived from clk, never from a status literal: it starts at the
// first status_transition INTO a state clk.Engaging accepts, ends at the
// first status_transition INTO a state clk.Terminal accepts (a park/blocked
// transition does NOT stop the clock, since the story is still open), or now
// when it has not yet reached one.
//
// haveEngage is proven ONLY by a logged engage transition — never guessed at
// from item's current status. A story with no such transition has never
// engaged as far as the ledger can prove, whatever its present status: a
// story cancelled straight out of backlog (backlog -> cancelled, no engaging
// state ever entered) and a legacy item whose transition history predates
// this accounting (done/cancelled with no transitions logged at all) both
// read as unavailable rather than a fabricated CreatedAt-based span — item's
// CreatedAt is never a stand-in for when work actually started.
func Own(item workitem.Item, entries []ledger.Entry, clk Clock, now time.Time) Story {
	acc := &accumulator{}
	var engageAt time.Time
	haveEngage := false
	var terminalAt time.Time
	haveTerminal := false
	var rows []Row
	var driverRows []DriverRow

	for _, e := range entries {
		switch e.Kind {
		case ledger.KindAgentInvocation:
			row, ok := DecodeRow(e)
			if !ok {
				continue
			}
			acc.addDispatchRow(row)
			rows = append(rows, row)
		case ledger.KindDriverUsage:
			d, ok := DecodeDriverRow(e)
			if !ok {
				continue
			}
			acc.addDriverRow(d)
			driverRows = append(driverRows, d)
		case ledger.KindStatusTransition:
			var p struct {
				From string `json:"from"`
				To   string `json:"to"`
			}
			if json.Unmarshal(e.Payload, &p) != nil {
				continue
			}
			if !haveEngage && clk.Engaging != nil && clk.Engaging(p.To) {
				engageAt, haveEngage = e.CreatedAt, true
			}
			if !haveTerminal && clk.Terminal != nil && clk.Terminal(p.To) {
				terminalAt, haveTerminal = e.CreatedAt, true
			}
		}
	}

	figures := acc.figures()
	var span *Span
	if haveEngage {
		end := now
		if haveTerminal {
			end = terminalAt
		}
		if end.After(engageAt) {
			figures.ElapsedMs = end.Sub(engageAt).Milliseconds()
		}
		span = &Span{Start: engageAt, End: end}
	} else {
		figures.ElapsedMs = -1 // never engaged — not started, not a measured zero
	}
	return Story{
		ID:         item.ID,
		Figures:    figures,
		Rows:       rows,
		DriverRows: driverRows,
		Span:       span,
		Estimates:  ParseEstimates(item.Tags),
	}
}
