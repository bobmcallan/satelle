package verb_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

// TestComputeStoryCost pins the cost rollup (sty_a699ad14 / sty_56aae77a):
// measured rows sum into TotalTokens; unreported rows are counted but do not
// feed the total; a measured zero still participates.
func TestComputeStoryCost(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	defer verb.SetLedgerStore(nil)
	verb.SetTxRunner(nil)

	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	// Legacy payloads without usage_available: non-zero total ⇒ measured.
	cost := func(from, to, model string, in, out int, dur int64) json.RawMessage {
		b, _ := json.Marshal(map[string]any{
			"from": from, "to": to, "agent": "reviewer", "skill": "gate-" + to, "model": model,
			"tokens_in": in, "tokens_out": out, "tokens_total": in + out, "duration_ms": dur,
		})
		return b
	}
	appendInv := func(payload json.RawMessage) {
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
			StoryID: "sty_cost1", Kind: ledger.KindAgentInvocation, Actor: "reviewer", Body: "invoked", Payload: payload,
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	appendInv(cost("plan", "in_progress", "glm-4.7", 100, 200, 3000))
	appendInv(cost("in_progress", "integration", "glm-5-turbo", 50, 60, 1500))
	// Uninstrumented (no usage field, zero tokens) — unreported, not free.
	appendInv(json.RawMessage(`{"from":"integration","to":"release","agent":"reviewer"}`))
	// Explicit measured zero.
	appendInv(json.RawMessage(`{"from":"release","to":"done","agent":"reviewer","tokens_total":0,"usage_available":true,"duration_ms":100}`))
	// Explicit unreported with zero tokens.
	appendInv(json.RawMessage(`{"from":"a","to":"b","agent":"reviewer","tokens_total":0,"usage_available":false,"duration_ms":50}`))

	sc, err := verb.ComputeStoryCost(ctx, "sty_cost1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(sc.Rows))
	}
	// Measured: 300 + 110 + 0 (explicit zero) = 410. Unreported rows excluded.
	if sc.TotalTokens != 410 {
		t.Errorf("total tokens = %d, want 410 (measured only)", sc.TotalTokens)
	}
	if sc.MeasuredRows != 3 {
		t.Errorf("measured rows = %d, want 3", sc.MeasuredRows)
	}
	if sc.UnmeasuredRows != 2 {
		t.Errorf("unmeasured rows = %d, want 2", sc.UnmeasuredRows)
	}
	if sc.TotalDurationMs != 4650 { // 3000+1500+0+100+50
		t.Errorf("total duration = %d, want 4650", sc.TotalDurationMs)
	}
	var found bool
	for _, r := range sc.Rows {
		if r.Model == "glm-4.7" && r.TokensTotal == 300 && r.TokensIn == 100 && r.UsageAvailable {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a measured glm-4.7 row with 300 tokens: %+v", sc.Rows)
	}
	// Uninstrumented row must be UsageAvailable=false.
	var unreported bool
	for _, r := range sc.Rows {
		if r.From == "integration" && r.To == "release" && !r.UsageAvailable {
			unreported = true
		}
	}
	if !unreported {
		t.Errorf("expected unreported integration→release row: %+v", sc.Rows)
	}
}

// TestComputeStoryCostSteps pins the per-step report (sty_3b2e55f5): each step's
// WALL-TIME is derived from the deltas between status_transition timestamps — so
// IN-LOOP steps (which spawn no measurable subprocess) get a duration — and a
// step_cost entry merges its self-reported actual tokens + per-step estimate onto
// the matching step. A re-entered state sums its occupancy.
func TestComputeStoryCostSteps(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	defer verb.SetLedgerStore(nil)
	verb.SetTxRunner(nil)

	ctx := context.Background()
	t0 := time.Unix(1_700_000_000, 0)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	appendKind := func(kind string, payload json.RawMessage, when time.Time) {
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
			StoryID: "sty_steps", Kind: kind, Actor: "executor", Body: "x", Payload: payload,
		}, when); err != nil {
			t.Fatal(err)
		}
	}
	trans := func(from, to string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"from": from, "to": to})
		return b
	}

	// Lifecycle timeline: backlog occupied 60s, plan 120s, in_progress 300s (with a
	// recovery re-entry adding 40s = 340s total), integration 20s; done is terminal.
	appendKind(ledger.KindStoryCreated, json.RawMessage(`{}`), at(0))
	appendKind(ledger.KindStatusTransition, trans("backlog", "plan"), at(60))
	appendKind(ledger.KindStatusTransition, trans("plan", "in_progress"), at(180))        // plan: 120s
	appendKind(ledger.KindStatusTransition, trans("in_progress", "integration"), at(480)) // in_progress: 300s
	appendKind(ledger.KindStatusTransition, trans("integration", "in_progress"), at(500)) // integration: 20s (recovery)
	appendKind(ledger.KindStatusTransition, trans("in_progress", "done"), at(540))        // in_progress again: 40s
	// A step_cost for in_progress: self-reported actual tokens + a per-step estimate.
	scPayload, _ := json.Marshal(map[string]any{
		"step": "in_progress", "tokens_total": 42000, "est_tokens": 50000, "est_duration_ms": 2_400_000,
	})
	appendKind(ledger.KindStepCost, scPayload, at(541))

	sc, err := verb.ComputeStoryCost(ctx, "sty_steps")
	if err != nil {
		t.Fatal(err)
	}
	steps := map[string]verb.StoryStepRow{}
	for _, s := range sc.Steps {
		steps[s.Step] = s
	}
	// Derived wall-time per step.
	if got := steps["backlog"].WallTimeMs; got != 60_000 {
		t.Errorf("backlog wall = %d ms, want 60000", got)
	}
	if got := steps["plan"].WallTimeMs; got != 120_000 {
		t.Errorf("plan wall = %d ms, want 120000", got)
	}
	// in_progress is entered twice (300s + 40s) — occupancy sums.
	if got := steps["in_progress"].WallTimeMs; got != 340_000 {
		t.Errorf("in_progress wall = %d ms, want 340000 (summed re-entry)", got)
	}
	if got := steps["integration"].WallTimeMs; got != 20_000 {
		t.Errorf("integration wall = %d ms, want 20000", got)
	}
	// done is terminal — no post-entry work, so no step row for it.
	if _, ok := steps["done"]; ok {
		t.Errorf("terminal 'done' must not accrue a step row: %+v", sc.Steps)
	}
	// step_cost merged onto in_progress: actual tokens + estimate.
	ip := steps["in_progress"]
	if !ip.HasTokens || ip.TokensTotal != 42000 {
		t.Errorf("in_progress actual tokens = %d (has=%v), want 42000/true", ip.TokensTotal, ip.HasTokens)
	}
	if ip.EstTokens != 50000 || ip.EstDurationMs != 2_400_000 {
		t.Errorf("in_progress estimate = %d tok / %d ms, want 50000 / 2400000", ip.EstTokens, ip.EstDurationMs)
	}
	// A step with no step_cost has no self-reported tokens (unmeasured, not zero).
	if steps["plan"].HasTokens {
		t.Errorf("plan had no step_cost; HasTokens must be false: %+v", steps["plan"])
	}
}

// TestComputeStoryCostStepCostDurationOverride: an explicit --time on a step_cost
// overrides the derived wall-time for that step.
func TestComputeStoryCostStepCostDurationOverride(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	defer verb.SetLedgerStore(nil)
	verb.SetTxRunner(nil)

	ctx := context.Background()
	t0 := time.Unix(1_700_000_000, 0)
	app := func(kind string, payload json.RawMessage, when time.Time) {
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: "sty_ovr", Kind: kind, Actor: "executor", Body: "x", Payload: payload}, when); err != nil {
			t.Fatal(err)
		}
	}
	app(ledger.KindStoryCreated, json.RawMessage(`{}`), t0)
	app(ledger.KindStatusTransition, json.RawMessage(`{"from":"in_progress","to":"done"}`), t0.Add(100*time.Second)) // derived 100s
	app(ledger.KindStepCost, json.RawMessage(`{"step":"in_progress","duration_ms":5000}`), t0.Add(101*time.Second))

	sc, err := verb.ComputeStoryCost(ctx, "sty_ovr")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sc.Steps {
		if s.Step == "in_progress" && s.WallTimeMs != 5000 {
			t.Errorf("explicit step-cost --time must override derived wall-time: got %d ms, want 5000", s.WallTimeMs)
		}
	}
}
