package web

import (
	"bytes"
	"encoding/json"
	"html"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func pricedWebFixture(t *testing.T, p costview.Pricing) (workitem.Item, costview.Story, costview.FamilyCost) {
	t.Helper()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := func(storyID, agent, model string, fresh, out int) ledger.Entry {
		raw, _ := json.Marshal(map[string]any{"agent": agent, "model_resolved": model, "adapter": "claude", "usage_available": true,
			"tokens_in_fresh": fresh, "tokens_in": fresh, "tokens_out": out})
		return ledger.Entry{StoryID: storyID, Kind: ledger.KindAgentInvocation, Payload: raw, CreatedAt: at}
	}
	drv, _ := json.Marshal(map[string]any{"session_id": "S", "executable": "grok", "available": true, "fresh_input": 10})
	root := workitem.Item{ID: "sty_root"}
	child := workitem.Item{ID: "sty_child", ParentID: "sty_root"}
	entries := map[string][]ledger.Entry{
		"sty_root": {inv("sty_root", "coder", "m-big", 1_000_000, 100_000),
			{StoryID: "sty_root", Kind: ledger.KindDriverUsage, Payload: drv, CreatedAt: at}},
		"sty_child": {inv("sty_child", "reviewer", "m-ghost", 5, 1)},
	}
	clk := func(workitem.Item) costview.Clock { return costview.Clock{} }
	own := costview.Own(root, entries["sty_root"], costview.Clock{}, at, costview.WithPricing(p))
	fam := costview.Family(root, []workitem.Item{root, child}, entries, clk, at, costview.WithPricing(p))
	return root, own, fam
}

func renderCost(t *testing.T, root workitem.Item, own costview.Story, fam *costview.FamilyCost) string {
	t.Helper()
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "itemDetail", detailData{Item: root, Cost: costVMFromStory(own, fam)}); err != nil {
		t.Fatal(err)
	}
	// html/template escapes "+"; assert on what the reader sees.
	return html.UnescapeString(buf.String())
}

// TestCostPanelShowsPricedSeatsAndFamily pins the web half of AC1/AC3/AC5/AC7/AC9:
// the story's cost panel shows the derived cost ahead of the band, a per-seat
// table, an unrecorded driver model named by its adapter, and the family
// roll-up with the unavailable part carried to the total.
func TestCostPanelShowsPricedSeatsAndFamily(t *testing.T) {
	p := costview.Pricing{Models: map[string]costview.Rates{"m-big": {FreshInput: 3, Output: 15}}}
	root, own, fam := pricedWebFixture(t, p)
	out := renderCost(t, root, own, &fam)

	for _, want := range []string{
		`<dt>Cost</dt><dd class="cost-derived">$4.5000 + unavailable (model unrecorded (adapter grok))</dd>`,
		`<td class="id">coder@m-big</td><td>1000000</td>`,
		`<td>$4.5000</td>`,
		`<td class="id">driver</td>`, // driver model unrecorded → plain "driver" seat
		"<h4>By seat</h4>",
		"<h4>Family by seat</h4>",
		`<th>Child</th><th>Cost</th><th>Band</th>`,
		`reviewer@m-ghost`,
		"model m-ghost has no rate",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("cost panel missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "<dt>Cost</dt>") > strings.Index(out, "<dt>Band</dt>") {
		t.Errorf("cost must lead the band:\n%s", out)
	}
	// the family total carries the unavailable child part
	if !strings.Contains(out, `<tr class="cost-family-total"><td>FAMILY TOTAL</td><td>$4.5000 + unavailable (`) {
		t.Errorf("family total hides the unavailable part:\n%s", out)
	}
}

// TestCostPanelSubscriptionLabelAndNoConfig pins the share label (AC4) and the
// unconfigured reading (AC5) on the web page.
func TestCostPanelSubscriptionLabelAndNoConfig(t *testing.T) {
	sub := costview.Pricing{Subscription: &costview.Subscription{FeeUSD: 10, TokensPerPeriod: 1_000_000}}
	root, own, _ := pricedWebFixture(t, sub)
	out := renderCost(t, root, own, nil)
	// 1,100,000 + 10 tokens of the 1M-token plan
	if !strings.Contains(out, "(subscription share, not a metered price)") {
		t.Errorf("subscription figure not labelled as a share:\n%s", out)
	}

	root, own, _ = pricedWebFixture(t, costview.Pricing{})
	out = renderCost(t, root, own, nil)
	if !strings.Contains(out, `<dd class="cost-derived">unavailable (no pricing configured)</dd>`) {
		t.Errorf("no [pricing] must read unavailable, never $0:\n%s", out)
	}
}
