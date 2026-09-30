package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func costSummaryOut(t *testing.T, sc verb.StoryCost) string {
	t.Helper()
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	printCostSummary(cmd, sc)
	return buf.String()
}

// figuresFromDriverRows folds driver rows into Figures through costview.Own —
// the same computation `story cost` reads — from real driver_usage ledger entries.
func figuresFromDriverRows(rows []costview.DriverRow) costview.Figures {
	var entries []ledger.Entry
	for _, r := range rows {
		raw, _ := json.Marshal(r)
		entries = append(entries, ledger.Entry{Kind: ledger.KindDriverUsage, Payload: raw})
	}
	return costview.Own(workitem.Item{ID: "sty_cov"}, entries, costview.Clock{}, time.Now()).Figures
}

// TestCostSummaryStatesDriverCoverage pins AC3: the cost report says whether the
// driving session is in the total — present, explicitly unavailable with the
// adapter's own reason, or explicitly absent — so the gated-and-dispatched total
// is never mistaken for the whole.
func TestCostSummaryStatesDriverCoverage(t *testing.T) {
	pi := costview.DriverRow{Executable: "pi", Available: false, UnavailableReason: "pi: session record for s not found under /x"}
	claude := costview.DriverRow{Executable: "claude", Available: true, FreshInput: 5}

	cases := []struct {
		name string
		rows []costview.DriverRow
		want []string
	}{
		{"unavailable", []costview.DriverRow{pi}, []string{"driver: unavailable — pi: session record for s not found under /x", "gated-and-dispatched"}},
		{"measured", []costview.DriverRow{claude}, []string{"driver: measured (claude 1 rows)"}},
		{"absent", nil, []string{"driver: none recorded", "gated-and-dispatched"}},
	}
	for _, c := range cases {
		out := costSummaryOut(t, verb.StoryCost{Figures: figuresFromDriverRows(c.rows)})
		for _, want := range c.want {
			if !strings.Contains(out, want) {
				t.Errorf("%s: cost summary missing %q; got:\n%s", c.name, want, out)
			}
		}
	}
}

// TestCostSummaryFamilyStatesPerAdapterCoverage pins AC4: the aggregate (family
// total) states, per adapter, what its driver rows measured and what they could
// not — for usage and for cost.
func TestCostSummaryFamilyStatesPerAdapterCoverage(t *testing.T) {
	total := figuresFromDriverRows([]costview.DriverRow{
		{Executable: "claude", Available: true, CostUnavailableReason: "claude: session transcript reports no cost field"},
		{Executable: "pi", Available: false, UnavailableReason: "pi: no session record", CostUnavailableReason: "pi: no session record"},
	})
	sc := verb.StoryCost{Figures: costview.Figures{}, Family: &costview.FamilyCost{Total: total}}
	out := costSummaryOut(t, sc)
	for _, want := range []string{
		"claude: driver usage measured (1 of 1 rows); driver cost unavailable (claude: session transcript reports no cost field)",
		"pi: driver usage unavailable (pi: no session record); driver cost unavailable (pi: no session record)",
		"driver: partial",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("family summary missing %q; got:\n%s", want, out)
		}
	}
}
