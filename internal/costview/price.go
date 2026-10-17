package costview

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// Pricing is the operator's [pricing] configuration (~/.satelle/config.toml),
// in exactly one of two modes: Models (rates mode — USD per million tokens, per
// token class, keyed by the model id the transport reported) or Subscription (a
// plan fee spread over the plan's token allowance). It carries no rate of its
// own: every number is the operator's, so a price or plan change re-prices every
// story on its next read and no ledger row is ever rewritten (sty_6267abb7).
type Pricing struct {
	Models       map[string]Rates `toml:"models"`
	Subscription *Subscription    `toml:"subscription"`
	// Invalid is set by a reader that could not load the configuration; the
	// derived cost then reads unavailable and says why, rather than silently
	// behaving as if nothing were configured.
	Invalid string `toml:"-"`
}

// Rates is one model's price in USD per million tokens, per token class.
type Rates struct {
	FreshInput float64 `toml:"fresh_input"`
	CacheRead  float64 `toml:"cache_read"`
	CacheWrite float64 `toml:"cache_write"`
	Output     float64 `toml:"output"`
}

// Subscription is a flat plan: FeeUSD buys TokensPerPeriod tokens, so a token
// is worth FeeUSD/TokensPerPeriod whatever model spent it.
type Subscription struct {
	FeeUSD          float64 `toml:"fee_usd"`
	TokensPerPeriod float64 `toml:"tokens_per_period"`
}

// Derived-cost modes. ModeNone is the zero value: nothing configured.
const (
	ModeNone         = ""
	ModeRates        = "rates"
	ModeSubscription = "subscription"
)

// SubscriptionShareLabel says a subscription figure is an allocation of the
// plan fee, not what any provider metered.
const SubscriptionShareLabel = "subscription share, not a metered price"

// Mode reports which pricing mode p configures.
func (p Pricing) Mode() string {
	switch {
	case p.Subscription != nil:
		return ModeSubscription
	case len(p.Models) > 0:
		return ModeRates
	default:
		return ModeNone
	}
}

// Option adjusts how Own and Family read a ledger.
type Option func(*Pricing)

// WithPricing prices the measured tokens under p at read time.
func WithPricing(p Pricing) Option { return func(dst *Pricing) { *dst = p } }

func pricingOf(opts []Option) Pricing {
	var p Pricing
	for _, o := range opts {
		o(&p)
	}
	return p
}

// DerivedCost is a set of measured tokens priced under Pricing. USD covers only
// the priced part; Unavailable names, one note each, every part that could not
// be priced — a token is never priced as zero for want of a rate, a model, or a
// cache split. PricedParts counts the parts that were priced.
type DerivedCost struct {
	Mode        string   `json:"mode,omitempty"`
	USD         float64  `json:"usd"`
	PricedParts int      `json:"priced_parts"`
	Unavailable []string `json:"unavailable,omitempty"`
}

func foldDerived(a, b DerivedCost) DerivedCost {
	out := DerivedCost{Mode: a.Mode, USD: a.USD + b.USD, PricedParts: a.PricedParts + b.PricedParts}
	if out.Mode == ModeNone {
		out.Mode = b.Mode
	}
	seen := map[string]bool{}
	for _, n := range append(append([]string(nil), a.Unavailable...), b.Unavailable...) {
		if !seen[n] {
			seen[n] = true
			out.Unavailable = append(out.Unavailable, n)
		}
	}
	sort.Strings(out.Unavailable)
	return out
}

// FormatDerived renders a derived cost for any surface: the priced figure, with
// an explicit unavailable note for any part that could not be priced.
func FormatDerived(d DerivedCost) string {
	notes := strings.Join(d.Unavailable, "; ")
	switch {
	case d.Mode == ModeNone && notes == "":
		return "unavailable (no pricing configured)"
	case d.PricedParts == 0 && notes == "":
		return "unavailable (no usage measured)"
	case d.PricedParts == 0:
		return "unavailable (" + notes + ")"
	case d.Mode == ModeSubscription:
		return fmt.Sprintf("$%.4f (%s)", d.USD, SubscriptionShareLabel)
	case notes == "":
		return fmt.Sprintf("$%.4f", d.USD)
	default:
		return fmt.Sprintf("$%.4f + unavailable (%s)", d.USD, notes)
	}
}

// split is a measured row's tokens by class. known is false when the class
// split was not reported, so it can be neither priced nor assumed all-fresh.
type split struct {
	fresh, cacheRead, cacheWrite, output int
	known                                bool
	why                                  string // why it is not known
}

// pricer prices rows as the accumulator folds them. Rates mode is priced per
// row; subscription mode is priced from the class sum in result.
type pricer struct {
	p     Pricing
	usd   float64
	parts int
	notes map[string]bool
}

func newPricer(p Pricing) pricer { return pricer{p: p} }

func (pr *pricer) note(s string) {
	if pr.notes == nil {
		pr.notes = map[string]bool{}
	}
	pr.notes[s] = true
}

// dispatch prices one measured agent_invocation row (rates mode only). A row
// with per-model entries prices each entry under its own id; a row without
// prices its row-level split under the seat's model.
func (pr *pricer) dispatch(row Row) {
	if pr.p.Mode() != ModeRates {
		return
	}
	if len(row.Models) == 0 {
		model := modelOf(row.Model, row.ModelResolved)
		pr.price(model, row.Adapter, rowSplit(row, row.TokensOut))
		return
	}
	for _, m := range row.Models {
		pr.price(m.ID, row.Adapter, entrySplit(m, row))
	}
}

// driver prices one measured driver_usage row (rates mode only) under the
// driving session's recorded model.
func (pr *pricer) driver(d DriverRow) {
	if pr.p.Mode() != ModeRates {
		return
	}
	pr.price(d.Model, d.Executable, split{
		fresh: d.FreshInput, cacheRead: d.CacheRead, cacheWrite: d.CacheWrite, output: d.Output, known: true,
	})
}

func (pr *pricer) price(model, adapter string, s split) {
	if !ledger.ModelRecorded(model) {
		if adapter == "" {
			adapter = "unrecorded"
		}
		pr.note("model unrecorded (adapter " + adapter + ")")
		return
	}
	rates, ok := pr.p.Models[model]
	if !ok {
		pr.note("model " + model + " has no rate")
		return
	}
	if !s.known {
		pr.note("model " + model + ": " + s.why)
		return
	}
	const perToken = 1e6
	pr.usd += (float64(s.fresh)*rates.FreshInput + float64(s.cacheRead)*rates.CacheRead +
		float64(s.cacheWrite)*rates.CacheWrite + float64(s.output)*rates.Output) / perToken
	pr.parts++
}

// rowSplit is a row's own class split. A row recorded before the split existed
// reports only a cache-inclusive total and is never priced as all-fresh.
func rowSplit(row Row, output int) split {
	if row.TokensInFresh > 0 || row.TokensCacheWrite > 0 || row.TokensCacheRead > 0 || row.TokensIn == 0 {
		return split{fresh: row.TokensInFresh, cacheRead: row.TokensCacheRead, cacheWrite: row.TokensCacheWrite, output: output, known: true}
	}
	return split{output: output, why: "unsplit legacy row"}
}

// entrySplit is one model_usage entry's class split: fresh = tokens_in -
// cache_write - cache_read. An entry whose own cache split was not reported
// borrows the row-level split only when it is the row's sole entry — the split
// then belongs to it — and otherwise reads unknown.
func entrySplit(m ledger.ModelTokens, row Row) split {
	if m.TokensCacheRead > 0 || m.TokensCacheWrite > 0 {
		fresh := m.TokensIn - m.TokensCacheWrite - m.TokensCacheRead
		if fresh < 0 {
			return split{why: "inconsistent cache split"}
		}
		return split{fresh: fresh, cacheRead: m.TokensCacheRead, cacheWrite: m.TokensCacheWrite, output: m.TokensOut, known: true}
	}
	if m.TokensIn == 0 {
		return split{output: m.TokensOut, known: true}
	}
	if len(row.Models) == 1 {
		if s := rowSplit(row, m.TokensOut); s.known {
			return s
		}
	}
	return split{output: m.TokensOut, why: "cache split not reported"}
}

// result is the derived cost of f, the tokens this pricer saw fold in.
func (pr *pricer) result(f Figures) DerivedCost {
	switch pr.p.Mode() {
	case ModeSubscription:
		s := pr.p.Subscription
		d := DerivedCost{Mode: ModeSubscription, PricedParts: f.UsageRows}
		if s.TokensPerPeriod > 0 {
			d.USD = s.FeeUSD * float64(f.TotalTokens()) / s.TokensPerPeriod
		}
		return d
	case ModeRates:
		d := DerivedCost{Mode: ModeRates, USD: pr.usd, PricedParts: pr.parts}
		for n := range pr.notes {
			d.Unavailable = append(d.Unavailable, n)
		}
		sort.Strings(d.Unavailable)
		return d
	default:
		if pr.p.Invalid != "" {
			return DerivedCost{Unavailable: []string{"pricing config invalid: " + pr.p.Invalid}}
		}
		return DerivedCost{}
	}
}

// SeatLine is the tokens and derived cost of one agent+model seat, named by
// seatOf: a dispatched agent's seat is agent@model, and the driving session's
// is driver@model — plain driver when its model was not recorded.
type SeatLine struct {
	Seat string `json:"seat"`
	// Auth is the auth mode of the seat's rows (ledger.AuthLabel; unrecorded for a
	// row that recorded none and for the driving session). A seat whose rows ran
	// under different modes lists them, comma-joined and sorted. It is separate
	// from Derived.Mode, which is how the tokens are priced.
	Auth                 string      `json:"auth"`
	FreshInput           int         `json:"fresh_input"`
	CacheRead            int         `json:"cache_read"`
	CacheWrite           int         `json:"cache_write"`
	Output               int         `json:"output"`
	UnsplitTokens        int         `json:"unsplit_tokens,omitempty"`
	UsageRows            int         `json:"usage_rows"`
	UnsplitRows          int         `json:"unsplit_rows,omitempty"`
	UsageUnavailableRows int         `json:"usage_unavailable_rows"`
	Derived              DerivedCost `json:"derived_cost"`
}

// DriverSeat is the agent half of the driving session's seat label.
const DriverSeat = "driver"

// Figures is the seat's token figures, for the shared token formatters.
func (s SeatLine) Figures() Figures {
	return Figures{
		FreshInput: s.FreshInput, CacheRead: s.CacheRead, CacheWrite: s.CacheWrite, Output: s.Output,
		UnsplitTokens: s.UnsplitTokens, UsageRows: s.UsageRows, UnsplitRows: s.UnsplitRows,
		UsageUnavailableRows: s.UsageUnavailableRows, Derived: s.Derived,
	}
}

func recordedModel(model, resolved string) string {
	if m := modelOf(model, resolved); ledger.ModelRecorded(m) {
		return m
	}
	return ""
}

// BySeat folds a story's own dispatch and driver rows into one line per
// agent+model seat, each from one accumulator — the same counting rule Own
// applies to the story total, so a bundled reviewer session counts once, whole,
// under its seat, and a superseded or baseline driver row adds no tokens.
func BySeat(rows []Row, drivers []DriverRow, p Pricing) []SeatLine {
	accs := map[string]*accumulator{}
	auths := map[string]string{}
	get := func(seat, auth string) *accumulator {
		a, ok := accs[seat]
		if !ok {
			a = &accumulator{price: newPricer(p)}
			accs[seat] = a
		}
		auths[seat] = mergeAuth(auths[seat], auth)
		return a
	}
	for _, row := range rows {
		get(seatOf(row.Agent, recordedModel(row.Model, row.ModelResolved)), ledger.AuthLabel(row.AuthMode)).addDispatchRow(row)
	}
	for _, d := range MarkSuperseded(append([]DriverRow(nil), drivers...)) {
		// The driving session is not a binding, so it declares no auth.
		get(seatOf(DriverSeat, recordedModel(d.Model, "")), ledger.AuthUnrecorded).addDriverRow(d)
	}
	lines := make([]SeatLine, 0, len(accs))
	for seat, a := range accs {
		f := a.figures()
		lines = append(lines, SeatLine{
			Seat: seat, Auth: auths[seat], FreshInput: f.FreshInput, CacheRead: f.CacheRead, CacheWrite: f.CacheWrite, Output: f.Output,
			UnsplitTokens: f.UnsplitTokens, UsageRows: f.UsageRows, UnsplitRows: f.UnsplitRows,
			UsageUnavailableRows: f.UsageUnavailableRows, Derived: f.Derived,
		})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].Seat < lines[j].Seat })
	return lines
}

// mergeAuth unions two comma-joined auth lists into a sorted, de-duplicated one.
func mergeAuth(a, b string) string {
	seen := map[string]bool{}
	var modes []string
	for _, list := range []string{a, b} {
		for _, m := range strings.Split(list, ",") {
			if m = strings.TrimSpace(m); m != "" && !seen[m] {
				seen[m] = true
				modes = append(modes, m)
			}
		}
	}
	sort.Strings(modes)
	return strings.Join(modes, ",")
}

// MergeSeats sums two seat breakdowns by seat — the epic roll-up of its
// children. An unavailable part on either side stays unavailable in the sum.
func MergeSeats(a, b []SeatLine) []SeatLine {
	byseat := map[string]SeatLine{}
	for _, l := range append(append([]SeatLine(nil), a...), b...) {
		cur, ok := byseat[l.Seat]
		if !ok {
			byseat[l.Seat] = l
			continue
		}
		cur.Auth = mergeAuth(cur.Auth, l.Auth)
		cur.FreshInput += l.FreshInput
		cur.CacheRead += l.CacheRead
		cur.CacheWrite += l.CacheWrite
		cur.Output += l.Output
		cur.UnsplitTokens += l.UnsplitTokens
		cur.UsageRows += l.UsageRows
		cur.UnsplitRows += l.UnsplitRows
		cur.UsageUnavailableRows += l.UsageUnavailableRows
		cur.Derived = foldDerived(cur.Derived, l.Derived)
		byseat[l.Seat] = cur
	}
	out := make([]SeatLine, 0, len(byseat))
	for _, l := range byseat {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seat < out[j].Seat })
	return out
}

// SeatLineView is a SeatLine pre-formatted once, so the CLI and the web page
// render identical strings for identical ledger data.
type SeatLineView struct {
	Seat, Auth, FreshIn, CacheRead, CacheWrite, Out, Cost string
}

// FormatSeatLines renders lines through the same token formatters the headline
// figures use.
func FormatSeatLines(lines []SeatLine) []SeatLineView {
	out := make([]SeatLineView, 0, len(lines))
	for _, l := range lines {
		f := l.Figures()
		splitRows := f.UsageRows - f.UnsplitRows
		out = append(out, SeatLineView{
			Seat:       l.Seat,
			Auth:       ledger.AuthLabel(l.Auth),
			FreshIn:    FormatSplitTokens(f.FreshInput, splitRows, f.UnsplitRows, f.UsageUnavailableRows),
			CacheRead:  FormatSplitTokens(f.CacheRead, splitRows, f.UnsplitRows, f.UsageUnavailableRows),
			CacheWrite: FormatSplitTokens(f.CacheWrite, splitRows, f.UnsplitRows, f.UsageUnavailableRows),
			Out:        FormatTokensMeasured(f.Output, f.UsageRows, f.UsageUnavailableRows),
			Cost:       FormatDerived(l.Derived),
		})
	}
	return out
}
