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

// TestComputeStoryCost pins the cost rollup (sty_a699ad14): agent_invocation
// entries with recorded token/duration payloads sum into a per-story view; an
// entry with no usage (a pre-instrumentation or plain-text invocation) contributes
// zero without breaking the rollup.
func TestComputeStoryCost(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	verb.SetLedgerStore(db.Ledger)
	defer verb.SetLedgerStore(nil)

	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
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
	// An entry with no usage payload (uninstrumented) — must not break the rollup.
	appendInv(json.RawMessage(`{"from":"integration","to":"release","agent":"reviewer"}`))

	sc, err := verb.ComputeStoryCost(ctx, "sty_cost1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(sc.Rows))
	}
	if sc.TotalTokens != 410 { // 300 + 110 + 0
		t.Errorf("total tokens = %d, want 410", sc.TotalTokens)
	}
	if sc.TotalDurationMs != 4500 {
		t.Errorf("total duration = %d, want 4500", sc.TotalDurationMs)
	}
	// Order-independent: a glm-4.7 row of 300 tokens is present (per-gate model +
	// tokens recorded).
	var found bool
	for _, r := range sc.Rows {
		if r.Model == "glm-4.7" && r.TokensTotal == 300 && r.TokensIn == 100 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a glm-4.7 row with 300 tokens: %+v", sc.Rows)
	}
}
