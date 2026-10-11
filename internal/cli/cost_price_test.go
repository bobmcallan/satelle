package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const priceRatesToml = `[pricing.models."m-big"]
fresh_input = 3.0
cache_read = 0.3
cache_write = 3.75
output = 15.0
`

// priceStoryRepo seeds a story with a coder@m-big dispatch row (1M fresh, 100k
// out: $3 + $1.50), a reviewer on a model with no configured rate, and a
// driving-session window on m-big (200k fresh: $0.60), and returns its id.
func priceStoryRepo(t *testing.T) string {
	t.Helper()
	_ = tempRepo(t)
	a, err := app.Open()
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	it, err := a.Store.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "priced", Status: "backlog"}, at)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(kind string, payload map[string]any) ledger.AppendInput {
		raw, _ := json.Marshal(payload)
		return ledger.AppendInput{StoryID: it.ID, Kind: kind, Actor: "test", Payload: raw}
	}
	for _, in := range []ledger.AppendInput{
		mk(ledger.KindAgentInvocation, map[string]any{"from": "plan", "to": "in_progress", "agent": "coder", "adapter": "claude",
			"model_resolved": "m-big", "usage_available": true, "tokens_in_fresh": 1_000_000, "tokens_in": 1_000_000, "tokens_out": 100_000, "cost_usd": 4.0}),
		mk(ledger.KindAgentInvocation, map[string]any{"from": "plan", "to": "in_progress", "agent": "reviewer", "adapter": "grok",
			"model_resolved": "m-ghost", "usage_available": true, "tokens_in_fresh": 10, "tokens_in": 10, "tokens_out": 1}),
		mk(ledger.KindDriverUsage, map[string]any{"session_id": "S1", "executable": "claude", "model": "m-big", "available": true,
			"fresh_input": 200_000, "trigger": "engage"}),
	} {
		if _, err := a.Store.Ledger.Append(ctx, in, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	return it.ID
}

func setPricing(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(config.GlobalDir(), config.GlobalConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStoryCostRatesModeEndToEnd pins AC1/AC3/AC5/AC8/AC9 through the real
// `satelle story cost` command: per-seat lines with tokens and cost, the
// derived total labelled `cost` and ahead of the band, provider dollars
// labelled `reported`, and the unpriced part named rather than zero.
func TestStoryCostRatesModeEndToEnd(t *testing.T) {
	id := priceStoryRepo(t)
	setPricing(t, priceRatesToml)

	out, err := runRoot(t, "story", "cost", id)
	if err != nil {
		t.Fatalf("story cost: %v\n%s", err, out)
	}
	// 1M fresh + 100k out on m-big = $4.50; driver 200k fresh = $0.60; reviewer unpriced.
	wantTotal := "$5.1000 + unavailable (model m-ghost has no rate)"
	if !regexp.MustCompile(`(?m)^cost\s+` + regexp.QuoteMeta(wantTotal)).MatchString(out) {
		t.Errorf("missing derived total %q:\n%s", wantTotal, out)
	}
	for _, want := range []string{"BY SEAT", "coder@m-big", "driver@m-big", "reviewer@m-ghost", "unavailable (model m-ghost has no rate)", "$4.5000", "$0.6000"} {
		if !strings.Contains(out, want) {
			t.Errorf("story cost output missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "\ncost") > strings.Index(out, "\nBAND") || !strings.Contains(out, "\nBAND") {
		t.Errorf("cost must lead the band:\n%s", out)
	}
	if !regexp.MustCompile(`(?m)^reported\s+\$4\.00`).MatchString(out) {
		t.Errorf("provider-reported dollars must be labelled reported:\n%s", out)
	}
	if !strings.Contains(out, "REPORTED $") {
		t.Errorf("driver table `$` column must be labelled REPORTED:\n%s", out)
	}
	// every dollar on a line is either derived (cost / seat table / total) or labelled reported
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "$") && !regexp.MustCompile(`(?i)cost|reported|\$[0-9.]+ \(`).MatchString(line) &&
			!strings.HasPrefix(line, "coder@") && !strings.HasPrefix(line, "driver@") {
			t.Errorf("unlabelled dollar figure: %q", line)
		}
	}
}

// TestStoryCostSubscriptionAndReprice pins AC4 (label) and AC6: the same
// ledger reads a different cost after [pricing] changes, with no ledger write.
func TestStoryCostSubscriptionAndReprice(t *testing.T) {
	id := priceStoryRepo(t)

	out, err := runRoot(t, "story", "cost", id)
	if err != nil || !regexp.MustCompile(`(?m)^cost\s+unavailable \(no pricing configured\)`).MatchString(out) {
		t.Fatalf("no [pricing]: err %v\n%s", err, out)
	}

	setPricing(t, "[pricing.subscription]\nfee_usd = 100\ntokens_per_period = 10000000\n")
	out, err = runRoot(t, "story", "cost", id)
	if err != nil {
		t.Fatal(err)
	}
	// class sum: 1M+100k + 10+1 + 200k = 1,300,011 tokens → 100 × 1300011 / 10M
	if !strings.Contains(out, "$13.0001 ("+costview.SubscriptionShareLabel+")") {
		t.Errorf("subscription share not labelled / computed:\n%s", out)
	}

	setPricing(t, strings.Replace(priceRatesToml, "fresh_input = 3.0", "fresh_input = 30.0", 1))
	out2, err := runRoot(t, "story", "cost", id)
	if err != nil {
		t.Fatal(err)
	}
	if out == out2 || !strings.Contains(out2, "$37.5000") { // 1.2M fresh × 30 + 100k out × 15 on m-big
		t.Errorf("changing [pricing] did not re-price the story:\n%s", out2)
	}
}

// TestStoryCostJSONCarriesDerivedCost pins AC8's JSON half: the existing keys
// stay and a derived_cost object is added.
func TestStoryCostJSONCarriesDerivedCost(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	raw, _ := json.Marshal(map[string]any{"agent": "coder", "model_resolved": "m-big", "usage_available": true,
		"tokens_in_fresh": 1_000_000, "tokens_in": 1_000_000, "cost_usd": 3.0})
	own := costview.Own(workitem.Item{ID: "sty_j"},
		[]ledger.Entry{{StoryID: "sty_j", Kind: ledger.KindAgentInvocation, Payload: raw, CreatedAt: at}},
		costview.Clock{}, at, costview.WithPricing(costview.Pricing{Models: map[string]costview.Rates{"m-big": {FreshInput: 3}}}))
	b, err := json.Marshal(verb.StoryCost{StoryID: "sty_j", TotalCostUSD: 3, Figures: own.Figures, Seats: own.Seats})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["total_cost_usd"]; !ok {
		t.Errorf("existing total_cost_usd key dropped: %s", b)
	}
	figs := got["figures"].(map[string]any)
	if _, ok := figs["cost_usd"]; !ok {
		t.Errorf("existing figures.cost_usd key dropped: %s", b)
	}
	dc, ok := figs["derived_cost"].(map[string]any)
	if !ok || dc["usd"] != float64(3) || dc["mode"] != "rates" {
		t.Errorf("figures.derived_cost = %v", figs["derived_cost"])
	}
	if seats, _ := got["seats"].([]any); len(seats) != 1 {
		t.Errorf("seats = %v", got["seats"])
	}
}
