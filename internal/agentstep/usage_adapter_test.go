package agentstep

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// The adapter that produced a usage rides every usage-carrying record, so an
// available usage still names its adapter (sty_58a9bdc8 AC2).
func TestUsageNoteNamesAdapter(t *testing.T) {
	n := usageNote(agentcli.UsageResult{Adapter: agentcli.HarnessPi, Available: true, CacheSplitAvailable: true})
	if n.Adapter != agentcli.HarnessPi || n.UsageUnavailableReason != "" {
		t.Fatalf("available pi note = %+v, want adapter pi and no unavailable reason", n)
	}
	row := map[string]any{}
	addUsageNote(row, agentcli.UsageResult{Adapter: agentcli.HarnessPi, Available: true, CacheSplitAvailable: true})
	if row["adapter"] != agentcli.HarnessPi || len(row) != 1 {
		t.Fatalf("available pi row = %+v, want only adapter=pi", row)
	}
	row = map[string]any{}
	addUsageNote(row, agentcli.UsageResult{Available: true, CacheSplitAvailable: true})
	if len(row) != 0 {
		t.Fatalf("a usage with no adapter grew fields: %+v", row)
	}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil || back["adapter"] != agentcli.HarnessPi {
		t.Fatalf("serialised note = %s, want adapter pi", b)
	}
}

// adapterUsageRunner is an attemptRunner that, like a pi command, reports its
// usage out of band (UsageRunner) with the adapter agentcli stamped on it.
type adapterUsageRunner struct {
	*attemptRunner
	usage agentcli.UsageResult
}

func (r adapterUsageRunner) RunUsage(ctx context.Context, req agentcli.Request) ([]byte, agentcli.UsageResult, error) {
	out, err := r.attemptRunner.Run(ctx, req)
	return out, r.usage, err
}

// An available pi usage reaches the agent-attempt ledger row, and the dispatch
// result's note, as adapter pi — across a repair, whose attempts are summed.
func TestAvailablePiUsageNamesAdapterOnLedgerRow(t *testing.T) {
	pi := adapterUsageRunner{
		attemptRunner: &attemptRunner{runs: []attemptRun{
			{out: validAttempt("draft")}, // missing AC2 -> repair
			{out: validAttempt("## AC1\nfixed\n## AC2\nfixed")},
		}},
		usage: agentcli.UsageResult{
			Adapter: agentcli.HarnessPi, Available: true, CacheSplitAvailable: true,
			InputTokens: 13, OutputTokens: 5, TotalTokens: 18, FreshInputTokens: 10, CacheReadInputTokens: 3,
			ModelResolved: "minimax/minimax-m3",
		},
	}
	g, events := attemptedEngine(t, attemptedDispatchSkill, nil, nil)
	g.newRunner = func(_iface, command string) (agentcli.Runner, error) { return pi, nil }
	g.SetArtifactAttacher(func(_ context.Context, _ workitem.Item, name, typ, _ string) (string, string, error) {
		return name, typ, nil
	})
	res, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan")
	if err != nil {
		t.Fatal(err)
	}
	if len(*events) != 2 {
		t.Fatalf("events = %#v, want one agent-attempt row per attempt", *events)
	}
	for i, ev := range *events {
		if ev.data["adapter"] != agentcli.HarnessPi {
			t.Errorf("attempt row %d adapter = %v, want pi: %+v", i, ev.data["adapter"], ev.data)
		}
		if ev.data["usage_available"] != true {
			t.Errorf("attempt row %d usage_available = %v, want true", i, ev.data["usage_available"])
		}
		if _, set := ev.data["usage_unavailable_reason"]; set {
			t.Errorf("attempt row %d carries an unavailable reason beside available usage", i)
		}
	}
	if res.UsageNote.Adapter != agentcli.HarnessPi {
		t.Fatalf("dispatch result note adapter = %q, want pi", res.UsageNote.Adapter)
	}
}

// A live session's close row sums its turns; the adapter survives the sum.
func TestLiveUsageTrackerKeepsAdapter(t *testing.T) {
	var tr liveUsageTracker
	h := tr.wrap(nil)
	h(agentcli.Event{Kind: agentcli.EventUsage, Usage: &agentcli.UsageResult{Adapter: agentcli.HarnessPi, Available: true, InputTokens: 1}})
	h(agentcli.Event{Kind: agentcli.EventUsage, Usage: &agentcli.UsageResult{Available: true, InputTokens: 2}})
	u, turns := tr.total()
	if u.Adapter != agentcli.HarnessPi || turns != 2 || u.InputTokens != 3 {
		t.Fatalf("total = %+v turns=%d, want adapter pi over 2 turns", u, turns)
	}
	row := map[string]any{}
	addUsageNote(row, u)
	if row["adapter"] != agentcli.HarnessPi {
		t.Fatalf("close-row note = %+v, want adapter pi", row)
	}
}
