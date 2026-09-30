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

// TestCostVMStatesDriverCoverage pins AC3/AC4 on the web surface: the Cost section
// says whether the driving session is in the figures and, for the family
// aggregate, what each adapter measured and could not — the same strings the CLI
// prints, because both come from costview's coverage formatters.
func TestCostVMStatesDriverCoverage(t *testing.T) {
	driver := func(storyID string, r costview.DriverRow) ledger.Entry {
		raw, _ := json.Marshal(r)
		return ledger.Entry{StoryID: storyID, Kind: ledger.KindDriverUsage, Payload: raw}
	}
	root := workitem.Item{ID: "sty_root"}
	child := workitem.Item{ID: "sty_child", ParentID: "sty_root"}
	entries := map[string][]ledger.Entry{
		"sty_root": {driver("sty_root", costview.DriverRow{Executable: "claude", Available: true, FreshInput: 5})},
		"sty_child": {driver("sty_child", costview.DriverRow{
			Executable: "pi", Available: false, UnavailableReason: "pi: session record not found",
			CostUnavailableReason: "pi: session record not found"})},
	}
	clk := func(workitem.Item) costview.Clock { return costview.Clock{} }
	now := time.Now()
	own := costview.Own(root, entries["sty_root"], clk(root), now)
	fam := costview.Family(root, []workitem.Item{root, child}, entries, clk, now)
	vm := costVMFromStory(own, &fam)

	if want := "driver: measured (claude 1 rows)"; vm.DriverLine != want {
		t.Fatalf("DriverLine = %q, want %q", vm.DriverLine, want)
	}
	if !strings.HasPrefix(vm.FamilyDriverLine, "driver: partial — pi: session record not found") {
		t.Fatalf("FamilyDriverLine = %q", vm.FamilyDriverLine)
	}
	wantPi := "pi: driver usage unavailable (pi: session record not found); driver cost unavailable (pi: session record not found)"
	var sawPi bool
	for _, l := range vm.FamilyAdapters {
		sawPi = sawPi || l == wantPi
	}
	if !sawPi || len(vm.FamilyAdapters) != 2 {
		t.Fatalf("FamilyAdapters = %q, want a claude and a pi line incl. %q", vm.FamilyAdapters, wantPi)
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "itemDetail", detailData{Item: root, Cost: vm}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{vm.DriverLine, vm.FamilyDriverLine, wantPi} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("itemDetail missing %q", want)
		}
	}

	// A story with no driver row at all says so, rather than saying nothing.
	empty := costVMFromStory(costview.Own(workitem.Item{ID: "sty_none"}, nil, costview.Clock{}, now), nil)
	if !strings.HasPrefix(empty.DriverLine, "driver: none recorded") {
		t.Fatalf("empty DriverLine = %q", empty.DriverLine)
	}
}
