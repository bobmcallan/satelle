package web

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// costFixture builds the same tiny family costview_test.go exercises for
// costview.Family directly — reused here so the CLI and web renderers are
// proven against ONE shared fixture (sty_b8542a3a AC7), not two independently
// hand-typed ones that could quietly drift apart.
func costFixture(t *testing.T) (workitem.Item, costview.Story, costview.FamilyCost) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := costview.Clock{
		Engaging: func(to string) bool { return to == "in_progress" },
		Terminal: func(to string) bool { return to == "done" },
	}
	priced := 1.5
	invoke := func(storyID string, at time.Time, cost *float64, fresh, out int) ledger.Entry {
		p := map[string]any{
			"from": "plan", "to": "in_progress", "agent": "coder", "skill": "coder", "model": "sonnet",
			"tokens_in_fresh": fresh, "tokens_out": out, "tokens_in": fresh, "tokens_total": fresh + out,
			"usage_available": true, "duration_ms": 1000,
		}
		if cost != nil {
			p["cost_usd"] = *cost
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		return ledger.Entry{StoryID: storyID, Kind: ledger.KindAgentInvocation, Payload: raw, CreatedAt: at}
	}
	transition := func(storyID, from, to string, at time.Time) ledger.Entry {
		raw, _ := json.Marshal(map[string]any{"from": from, "to": to})
		return ledger.Entry{StoryID: storyID, Kind: ledger.KindStatusTransition, Payload: raw, CreatedAt: at}
	}

	root := workitem.Item{ID: "sty_root", CreatedAt: base, Tags: []string{"estimate-usd:10"}}
	child := workitem.Item{ID: "sty_child", ParentID: "sty_root", CreatedAt: base}
	items := []workitem.Item{root, child}

	entriesByID := map[string][]ledger.Entry{
		"sty_root": {
			transition("sty_root", "backlog", "in_progress", base),
			transition("sty_root", "in_progress", "done", base.Add(time.Hour)),
			invoke("sty_root", base.Add(5*time.Minute), &priced, 100, 20),
		},
		"sty_child": {
			transition("sty_child", "backlog", "in_progress", base.Add(10*time.Minute)),
			transition("sty_child", "in_progress", "done", base.Add(20*time.Minute)),
			invoke("sty_child", base.Add(15*time.Minute), nil, 10, 2),
		},
	}
	now := base.Add(2 * time.Hour)
	own := costview.Own(root, entriesByID["sty_root"], clk, now)
	fam := costview.Family(root, items, entriesByID, func(workitem.Item) costview.Clock { return clk }, now)
	return root, own, fam
}

// TestCostVMRendersSharedFixture pins AC7: the web page's Cost section, built
// from the SAME costview.Story/FamilyCost a CLI-side test (internal/cli) also
// renders, shows the SAME formatted $, fresh, cache read/write, elapsed, agent
// time and family-total strings — proving the two surfaces read one
// computation rather than two that could drift.
func TestCostVMRendersSharedFixture(t *testing.T) {
	root, own, fam := costFixture(t)
	vm := costVMFromStory(own, &fam)

	d := detailData{Item: root, Cost: vm}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "itemDetail", d); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"Band",
		vm.Band,
		"110",      // fresh input (100 root + 10 child folded into the family total)
		"22",       // family output
		vm.Elapsed, // elapsed wall time — asserted non-empty below
		vm.AgentTime,
		"Fresh in",
		"Cache read",
		"Cache write",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("itemDetail missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "$") {
		t.Errorf("itemDetail must not show a dollar estimate or dollar actual:\n%s", out)
	}
	if vm.Elapsed == "" || vm.AgentTime == "" {
		t.Fatalf("vm = %+v, want non-empty Elapsed/AgentTime", vm)
	}
	if vm.Band != costview.FormatCostBand(own.Figures) || vm.Band != "low" {
		t.Errorf("Band = %q, want shared FormatCostBand %q (low)", vm.Band, costview.FormatCostBand(own.Figures))
	}
	if vm.FamilyTotal == nil || vm.FamilyTotal.Band != costview.FormatCostBand(fam.Total) {
		t.Errorf("FamilyTotal band = %+v, want %q", vm.FamilyTotal, costview.FormatCostBand(fam.Total))
	}
	if len(vm.Family) != 1 || vm.FamilyTotal == nil {
		t.Fatalf("vm.Family = %+v, FamilyTotal = %+v, want one child + a total", vm.Family, vm.FamilyTotal)
	}
}

// TestCostVMRendersDriverRow pins the AC4/AC7 fix: the web page's Cost
// section shows a driver-session row (its own line, split into fresh/output/
// cache read/cache write columns plus $ and agent time — never folded into
// one cache-inclusive figure), exactly matching what the CLI's DRIVER SESSION
// table renders for the same driver_usage data.
func TestCostVMRendersDriverRow(t *testing.T) {
	cost := 0.75
	own := costview.Story{
		ID: "sty_root",
		DriverRows: []costview.DriverRow{
			{
				SessionID: "sess1", Executable: "claude", Trigger: "engage",
				FreshInput: 50, Output: 10, CacheRead: 5, CacheWrite: 2,
				Available: true, WallSeconds: 30, CostUSD: &cost,
			},
			{SessionID: "sess2", Executable: "nosuch", Trigger: "close", Available: false, WallSeconds: 15},
		},
	}
	vm := costVMFromStory(own, nil)
	if len(vm.DriverRows) != 2 || vm.DriverTotal == nil {
		t.Fatalf("vm.DriverRows = %+v, DriverTotal = %+v, want 2 rows + a total", vm.DriverRows, vm.DriverTotal)
	}

	d := detailData{Item: workitem.Item{ID: "sty_root"}, Cost: vm}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "itemDetail", d); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Driver sessions", "sess1", "claude", "$0.75", "50", "10", "5", "2", "—"} {
		if !strings.Contains(out, want) {
			t.Errorf("itemDetail missing %q for the driver table; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "TOKENS") {
		t.Fatalf("driver table still shows a cache-inclusive TOKENS column; got:\n%s", out)
	}
}
