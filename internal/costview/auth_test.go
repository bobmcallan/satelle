package costview_test

import (
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// A seat's auth mode is shown in its own column, separate from how its tokens are
// priced: a row recorded with auth_mode shows it, a row written before it existed
// shows unrecorded, and a seat whose rows differ lists both. The cost column never
// carries an auth label, under either pricing mode.
func TestSeatAuthColumnIsSeparateFromPricingMode(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newRow := func(agent, model, auth string) ledger.Entry {
		payload := map[string]any{"agent": agent, "model_resolved": model, "adapter": "pi",
			"tokens_in_fresh": 1_000_000, "tokens_in": 1_000_000, "tokens_out": 100_000}
		if auth != "" {
			payload["auth_mode"] = auth
		}
		return rawInvocation(t, "sty_p", at, payload)
	}
	entries := []ledger.Entry{
		newRow("planner", "m-big", "api_key"),
		newRow("coder", "m-small", ""),  // written before auth_mode existed
		newRow("reviewer", "m-big", ""), // the same seat records two modes
		newRow("reviewer", "m-big", "org"),
	}
	subscription := costview.Pricing{Subscription: &costview.Subscription{FeeUSD: 20, TokensPerPeriod: 10_000_000}}

	for name, p := range map[string]costview.Pricing{"rates": ratesPricing(), "subscription": subscription} {
		t.Run(name, func(t *testing.T) {
			s := ownPriced(t, entries, p)
			for seat, want := range map[string]string{
				"planner@m-big":  "api_key",
				"coder@m-small":  ledger.AuthUnrecorded,
				"reviewer@m-big": "org,unrecorded",
			} {
				if got := seatByName(t, s.Seats, seat).Auth; got != want {
					t.Errorf("%s auth = %q, want %q", seat, got, want)
				}
			}
			for _, v := range costview.FormatSeatLines(s.Seats) {
				if v.Auth == "" {
					t.Errorf("seat %s renders no auth", v.Seat)
				}
				if strings.Contains(v.Cost, "api_key") || strings.Contains(v.Cost, "org") {
					t.Errorf("cost %q for seat %s carries an auth label: auth is its own column", v.Cost, v.Seat)
				}
			}
			if mode := s.Figures.Derived.Mode; (name == "rates" && mode != costview.ModeRates) || (name == "subscription" && mode != costview.ModeSubscription) {
				t.Errorf("pricing mode = %q under %s pricing: auth must not change it", mode, name)
			}
		})
	}

	merged := costview.MergeSeats(
		[]costview.SeatLine{{Seat: "a@m", Auth: "api_key"}},
		[]costview.SeatLine{{Seat: "a@m", Auth: "unrecorded"}, {Seat: "b@m"}})
	if merged[0].Auth != "api_key,unrecorded" {
		t.Errorf("merged auth = %q, want both modes", merged[0].Auth)
	}
	if views := costview.FormatSeatLines(merged); views[1].Auth != ledger.AuthUnrecorded {
		t.Errorf("a seat with no recorded auth renders %q, want %q", views[1].Auth, ledger.AuthUnrecorded)
	}
}
