package verb_test

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
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

// TestComputeStoryCostExcludesToolPermissionRows pins sty_8eae81ac AC4: a
// legacy agent_invocation row carrying the decided_by/decision/tool
// tool-permission shape must never surface as a cost row, and must never
// inflate UnmeasuredRows — the mechanism it used to before it got its own
// ledger kind.
func TestComputeStoryCostExcludesToolPermissionRows(t *testing.T) {
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
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: "sty_perm_cost", Kind: ledger.KindAgentInvocation, Actor: "coder",
		Payload: json.RawMessage(`{"tool":"Edit","kind":"permission","decision":"allow","decided_by":"policy"}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	// One real invocation alongside it, so a non-empty result still excludes
	// only the permission row.
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: "sty_perm_cost", Kind: ledger.KindAgentInvocation, Actor: "coder",
		Payload: json.RawMessage(`{"agent":"coder","tokens_total":100,"usage_available":true}`),
	}, now); err != nil {
		t.Fatal(err)
	}

	sc, err := verb.ComputeStoryCost(ctx, "sty_perm_cost")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (the permission row must be excluded)", len(sc.Rows))
	}
	if sc.UnmeasuredRows != 0 {
		t.Errorf("UnmeasuredRows = %d, want 0 — a permission row must never count as unmeasured", sc.UnmeasuredRows)
	}
	if sc.MeasuredRows != 1 {
		t.Errorf("MeasuredRows = %d, want 1", sc.MeasuredRows)
	}
}

// TestComputeStoryCostDollars pins sty_c4df7376 AC5: a row with its own
// cost_usd totals into TotalCostUSD; a row with no cost at all (nil, not
// zero) counts as uncosted; and a legacy row recorded before cost_usd existed,
// but carrying per-model costUSD, derives its cost from that per-model sum
// (ledger.EventTelemetry's fallback) rather than reporting unknown.
func TestComputeStoryCostDollars(t *testing.T) {
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
	appendInv := func(payload json.RawMessage) {
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
			StoryID: "sty_cost_usd", Kind: ledger.KindAgentInvocation, Actor: "reviewer", Body: "invoked", Payload: payload,
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	// A costed row with its own top-level cost_usd.
	costed, _ := json.Marshal(map[string]any{
		"from": "plan", "to": "in_progress", "agent": "reviewer",
		"usage_available": true, "cost_usd": 0.0095,
	})
	appendInv(costed)
	// A row whose provider reported no cost at all — nil, never a measured zero.
	uncosted, _ := json.Marshal(map[string]any{
		"from": "in_progress", "to": "integration", "agent": "reviewer",
		"usage_available": true, "cost_unavailable_reason": "codex command: no cost reported",
	})
	appendInv(uncosted)
	// A legacy row recorded before cost_usd existed, but with per-model costUSD —
	// EventTelemetry derives the invocation cost from the model sum.
	legacy, _ := json.Marshal(map[string]any{
		"from": "integration", "to": "release", "agent": "reviewer", "usage_available": true,
		"model_usage": []map[string]any{{"id": "claude-opus-5-5", "cost_usd": 0.1}},
	})
	appendInv(legacy)

	sc, err := verb.ComputeStoryCost(ctx, "sty_cost_usd")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(sc.Rows))
	}
	if sc.CostedRows != 2 || sc.UncostedRows != 1 {
		t.Errorf("costed/uncosted = %d/%d, want 2/1", sc.CostedRows, sc.UncostedRows)
	}
	want := 0.0095 + 0.1
	if diff := sc.TotalCostUSD - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("total cost = %v, want %v", sc.TotalCostUSD, want)
	}
	byEdge := map[string]verb.StoryCostRow{}
	for _, r := range sc.Rows {
		byEdge[r.From+"→"+r.To] = r
	}
	if r := byEdge["plan→in_progress"]; r.CostUSD == nil || *r.CostUSD != 0.0095 {
		t.Errorf("costed row = %+v, want cost_usd=0.0095", r)
	}
	if r := byEdge["in_progress→integration"]; r.CostUSD != nil || r.CostUnavailableReason == "" {
		t.Errorf("uncosted row = %+v, want nil cost with a reason", r)
	}
	if r := byEdge["integration→release"]; r.CostUSD == nil || *r.CostUSD != 0.1 {
		t.Errorf("legacy per-model row = %+v, want cost derived to 0.1", r)
	}
}

// TestComputeSkillRollup pins sty_363eaf55 AC4: the --by-skill roll-up
// aggregates agent_invocation rows across STORIES by skill, keeping the
// fresh/cache-write/cache-read split separate from legacy "unsplit" input so
// a row recorded before the split existed still reconciles into the total.
func TestComputeSkillRollup(t *testing.T) {
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
	appendInv := func(storyID string, payload json.RawMessage) {
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
			StoryID: storyID, Kind: ledger.KindAgentInvocation, Actor: "reviewer", Body: "invoked", Payload: payload,
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	// Two stories, same skill, split usage — must aggregate across stories.
	split := func(skill string, fresh, write, read, out, sysBytes, payloadBytes int) json.RawMessage {
		b, _ := json.Marshal(map[string]any{
			"agent": "reviewer", "skill": skill,
			"tokens_in": fresh + write + read, "tokens_out": out, "tokens_total": fresh + write + read + out,
			"tokens_in_fresh": fresh, "tokens_cache_write": write, "tokens_cache_read": read,
			"usage_available": true, "system_prompt_bytes": sysBytes, "payload_bytes": payloadBytes,
		})
		return b
	}
	appendInv("sty_a", split("satelle-story-done-review", 100, 30, 20, 10, 1000, 200))
	appendInv("sty_b", split("satelle-story-done-review", 50, 10, 5, 5, 2000, 400))
	// A legacy row (pre-split) on a different skill, same and other story.
	legacy, _ := json.Marshal(map[string]any{
		"agent": "reviewer", "skill": "satelle-story-plan-review",
		"tokens_in": 500, "tokens_out": 50, "tokens_total": 550, "usage_available": true,
	})
	appendInv("sty_a", legacy)
	// An unmeasured row must not feed any total.
	unmeasured, _ := json.Marshal(map[string]any{"agent": "reviewer", "skill": "satelle-story-plan-review", "usage_available": false})
	appendInv("sty_b", unmeasured)

	// --all: both skills, summed across both stories.
	rollup, err := verb.ComputeSkillRollup(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rollup.Rows) != 2 {
		t.Fatalf("rows = %d, want 2 skills: %+v", len(rollup.Rows), rollup.Rows)
	}
	var done, plan *verb.SkillRollupRow
	for i := range rollup.Rows {
		switch rollup.Rows[i].Skill {
		case "satelle-story-done-review":
			done = &rollup.Rows[i]
		case "satelle-story-plan-review":
			plan = &rollup.Rows[i]
		}
	}
	if done == nil || plan == nil {
		t.Fatalf("expected both skills present: %+v", rollup.Rows)
	}
	if done.Invocations != 2 || done.FreshTokens != 150 || done.CacheWriteTokens != 40 || done.CacheReadTokens != 25 ||
		done.TotalInputTokens != 215 || done.TotalOutputTokens != 15 {
		t.Errorf("done-review rollup = %+v, want invocations=2 fresh=150 write=40 read=25 in=215 out=15", done)
	}
	if done.AvgSystemPromptBytes != 1500 || done.AvgPayloadBytes != 300 {
		t.Errorf("done-review avg bytes = sys=%d payload=%d, want 1500/300", done.AvgSystemPromptBytes, done.AvgPayloadBytes)
	}
	if plan.Invocations != 2 || plan.MeasuredRows != 1 || plan.UnmeasuredRows != 1 ||
		plan.UnsplitTokens != 500 || plan.FreshTokens != 0 || plan.TotalInputTokens != 500 {
		t.Errorf("plan-review rollup = %+v, want invocations=2 measured=1 unmeasured=1 unsplit=500 total=500", plan)
	}

	// --story sty_a: scoped to one story only.
	scoped, err := verb.ComputeSkillRollup(ctx, "sty_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Rows) != 2 {
		t.Fatalf("scoped rows = %d, want 2: %+v", len(scoped.Rows), scoped.Rows)
	}
	for _, r := range scoped.Rows {
		if r.Skill == "satelle-story-done-review" && r.Invocations != 1 {
			t.Errorf("scoped done-review invocations = %d, want 1 (only sty_a's row)", r.Invocations)
		}
	}
}

// TestComputeSkillRollupPagesBeyondOnePage pins the integration-review rework
// of sty_363eaf55 AC4: --all must not silently drop rows past the first page
// when the ledger holds more agent_invocation rows than a single query page.
// ledger.ForEachKindPageSize is lowered as a test seam so pagination across
// several pages (including a final partial page, where the newest rows land)
// is exercised without seeding thousands of real rows.
// TestComputeSkillRollupDollars pins the --by-skill dollar roll-up
// (sty_c4df7376 AC5): costed invocations sum across stories, a legacy row's
// per-model cost counts, and a row with no cost is counted as uncosted —
// never folded into the total as zero.
func TestComputeSkillRollupDollars(t *testing.T) {
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
	appendInv := func(storyID string, payload map[string]any) {
		b, _ := json.Marshal(payload)
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
			StoryID: storyID, Kind: ledger.KindAgentInvocation, Actor: "reviewer", Body: "invoked", Payload: b,
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	appendInv("sty_a", map[string]any{"agent": "reviewer", "skill": "satelle-story-done-review", "usage_available": true, "cost_usd": 0.01})
	appendInv("sty_b", map[string]any{"agent": "reviewer", "skill": "satelle-story-done-review", "usage_available": true, "cost_usd": 0.02})
	appendInv("sty_b", map[string]any{"agent": "reviewer", "skill": "satelle-story-done-review", "usage_available": true, "cost_unavailable_reason": "codex command: no cost reported"})
	appendInv("sty_a", map[string]any{
		"agent": "reviewer", "skill": "satelle-story-plan-review", "usage_available": true,
		"model_usage": []map[string]any{{"id": "claude-opus-5-5", "cost_usd": 0.1}},
	})
	appendInv("sty_a", map[string]any{"agent": "reviewer", "skill": "satelle-story-plan-review", "usage_available": false})
	// A skill with no costed row at all: its costed count (Invocations -
	// UncostedRows) is zero, which the CLI renders as "unknown", not "$0".
	appendInv("sty_a", map[string]any{"agent": "reviewer", "skill": "satelle-story-intent-review", "usage_available": true, "cost_unavailable_reason": "codex command: no cost reported"})

	rollup, err := verb.ComputeSkillRollup(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	bySkill := map[string]verb.SkillRollupRow{}
	for _, r := range rollup.Rows {
		bySkill[r.Skill] = r
	}
	done, plan := bySkill["satelle-story-done-review"], bySkill["satelle-story-plan-review"]
	if math.Abs(done.TotalCostUSD-0.03) > 1e-12 || done.UncostedRows != 1 {
		t.Errorf("done-review: total $%v uncosted %d, want $0.03 and 1", done.TotalCostUSD, done.UncostedRows)
	}
	if math.Abs(plan.TotalCostUSD-0.1) > 1e-12 || plan.UncostedRows != 1 {
		t.Errorf("plan-review: total $%v uncosted %d, want $0.1 (legacy per-model) and 1 (unmeasured)", plan.TotalCostUSD, plan.UncostedRows)
	}
	intent := bySkill["satelle-story-intent-review"]
	if intent.Invocations != 1 || intent.UncostedRows != 1 || intent.Invocations-intent.UncostedRows != 0 || intent.TotalCostUSD != 0 {
		t.Errorf("intent-review = %+v, want 1 invocation, 1 uncosted, costed count 0 (renders unknown)", intent)
	}

	// Scoped to one story, only that story's rows feed the dollars.
	one, err := verb.ComputeSkillRollup(ctx, "sty_b")
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Rows) != 1 || math.Abs(one.Rows[0].TotalCostUSD-0.02) > 1e-12 || one.Rows[0].UncostedRows != 1 {
		t.Errorf("sty_b rollup = %+v, want one done-review row at $0.02 with 1 uncosted", one.Rows)
	}
}

func TestComputeSkillRollupPagesBeyondOnePage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	defer verb.SetLedgerStore(nil)
	verb.SetTxRunner(nil)

	orig := ledger.ForEachKindPageSize
	ledger.ForEachKindPageSize = 3
	defer func() { ledger.ForEachKindPageSize = orig }()

	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)
	const rowCount = 10 // 4 pages at page size 3 (3,3,3,1) — exercises a trailing partial page
	for i := 0; i < rowCount; i++ {
		payload, _ := json.Marshal(map[string]any{
			"agent": "reviewer", "skill": "satelle-story-plan-review",
			"tokens_in": i + 1, "tokens_out": 0, "tokens_total": i + 1, "usage_available": true,
		})
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
			StoryID: "sty_page", Kind: ledger.KindAgentInvocation, Actor: "reviewer", Body: "invoked", Payload: payload,
		}, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	rollup, err := verb.ComputeSkillRollup(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rollup.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 skill: %+v", len(rollup.Rows), rollup.Rows)
	}
	// Sum of tokens_in 1..10 = 55. If the scan stopped after the first page
	// (3 of 10 rows, oldest-first) this would be 1+2+3=6 — the newest rows,
	// including the whole trailing partial page, must be counted too.
	if rollup.Rows[0].Invocations != rowCount || rollup.Rows[0].UnsplitTokens != 55 {
		t.Errorf("rollup = %+v, want invocations=%d unsplit=55 (all pages counted, not just the first)",
			rollup.Rows[0], rowCount)
	}
}

// TestComputeStoryCostRecordsResolvedModel pins the "story cost" surface of
// AC6/AC7 (sty_87b86044): a row's ModelResolved is read straight off the
// agent_invocation payload (via the shared ledger.EventTelemetry reader), so
// `satelle story cost` renders the resolved id via ledger.ModelLabel when one
// was recorded, the alias marked unknown when only that was, and a bare
// "unknown" for a legacy row that predates both fields — without back-filling
// the stored payload.
func TestComputeStoryCostRecordsResolvedModel(t *testing.T) {
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
	appendInv := func(payload json.RawMessage) {
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
			StoryID: "sty_cost_resolved", Kind: ledger.KindAgentInvocation, Actor: "reviewer", Body: "invoked", Payload: payload,
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	resolved, _ := json.Marshal(map[string]any{
		"from": "plan", "to": "in_progress", "agent": "reviewer", "model": "opus",
		"model_resolved": "claude-opus-5-5", "usage_available": true,
	})
	appendInv(resolved)
	aliasOnly, _ := json.Marshal(map[string]any{
		"from": "in_progress", "to": "integration", "agent": "reviewer", "model": "opus", "usage_available": true,
	})
	appendInv(aliasOnly)
	// Legacy row: predates the model field entirely.
	appendInv(json.RawMessage(`{"from":"integration","to":"release","agent":"reviewer"}`))

	sc, err := verb.ComputeStoryCost(ctx, "sty_cost_resolved")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(sc.Rows))
	}
	byEdge := map[string]verb.StoryCostRow{}
	for _, r := range sc.Rows {
		byEdge[r.From+"→"+r.To] = r
	}
	if r, ok := byEdge["plan→in_progress"]; !ok {
		t.Fatalf("missing plan→in_progress row: %+v", sc.Rows)
	} else if got := ledger.ModelLabel(r.Model, r.ModelResolved); got != "claude-opus-5-5" {
		t.Errorf("resolved row label = %q, want claude-opus-5-5", got)
	}
	if r, ok := byEdge["in_progress→integration"]; !ok {
		t.Fatalf("missing in_progress→integration row: %+v", sc.Rows)
	} else if got := ledger.ModelLabel(r.Model, r.ModelResolved); got != "opus (unknown)" {
		t.Errorf("alias-only row label = %q, want opus (unknown)", got)
	}
	if r, ok := byEdge["integration→release"]; !ok {
		t.Fatalf("missing integration→release row: %+v", sc.Rows)
	} else if got := ledger.ModelLabel(r.Model, r.ModelResolved); got != "unknown" {
		t.Errorf("legacy row label = %q, want unknown", got)
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

// TestComputeStoryCostFamilyFiguresAndEstimates pins the costview-backed part
// of ComputeStoryCost (sty_b8542a3a AC1/AC3/AC5): a parent's own Figures are
// its own rows only (dollars = known-cost sum, uncosted counted not zeroed,
// fresh/output/cache split), its plan estimates keep their own units, and its
// Family lists the child and a total that folds both — while a childless
// story carries no Family at all.
func TestComputeStoryCostFamilyFiguresAndEstimates(t *testing.T) {
	db := wireActualWF(t)
	ctx := context.Background()
	parent, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "epic", Status: "backlog",
		Tags: []string{"estimate-usd:25", "estimate-tokens:9000"},
	}, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	child, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "child", Status: "backlog", ParentID: parent.ID,
	}, time.Unix(1_700_000_010, 0))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	appendInvocation(t, db, parent.ID, map[string]any{
		"from": "a", "to": "b", "agent": "coder", "usage_available": true,
		"tokens_in_fresh": 100, "tokens_out": 20, "tokens_cache_read": 30, "tokens_cache_write": 4,
		"cost_usd": 1.5,
	}, now)
	appendInvocation(t, db, parent.ID, map[string]any{
		"from": "b", "to": "c", "agent": "coder", "usage_available": true,
		"tokens_in_fresh": 10, "tokens_out": 2, // no cost_usd: unavailable, never $0
	}, now)
	appendInvocation(t, db, child.ID, map[string]any{
		"from": "a", "to": "b", "agent": "coder", "usage_available": true,
		"tokens_in_fresh": 1000, "tokens_out": 200, "cost_usd": 2.0,
	}, now)

	sc, err := verb.ComputeStoryCost(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := sc.Figures
	if f.CostUSD != 1.5 || f.CostRows != 1 || f.CostUnavailableRows != 1 {
		t.Errorf("own dollars = %v (%d costed, %d unavailable), want 1.5 (1 costed, 1 unavailable)", f.CostUSD, f.CostRows, f.CostUnavailableRows)
	}
	if f.FreshInput != 110 || f.Output != 22 || f.CacheRead != 30 || f.CacheWrite != 4 {
		t.Errorf("own tokens fresh/out/read/write = %d/%d/%d/%d, want 110/22/30/4 (child excluded, cache write not folded)",
			f.FreshInput, f.Output, f.CacheRead, f.CacheWrite)
	}

	units := map[string]float64{}
	for _, e := range sc.Estimates {
		units[e.Unit] = e.Value
	}
	if units["usd"] != 25 || units["tokens"] != 9000 || len(units) != 2 {
		t.Errorf("estimates = %+v, want usd:25 and legacy tokens:9000, each in its own unit", sc.Estimates)
	}

	if sc.Family == nil {
		t.Fatal("a parent with a child must carry a Family roll-up")
	}
	if len(sc.Family.Children) != 1 || sc.Family.Children[0].ID != child.ID {
		t.Fatalf("Family.Children = %+v, want just %s", sc.Family.Children, child.ID)
	}
	tot := sc.Family.Total
	if tot.CostUSD != 3.5 || tot.CostRows != 2 || tot.CostUnavailableRows != 1 {
		t.Errorf("family dollars = %v (%d costed, %d unavailable), want 3.5 (2 costed, 1 unavailable)", tot.CostUSD, tot.CostRows, tot.CostUnavailableRows)
	}
	if tot.FreshInput != 1110 || tot.Output != 222 {
		t.Errorf("family fresh/out = %d/%d, want 1110/222", tot.FreshInput, tot.Output)
	}

	leaf, err := verb.ComputeStoryCost(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Family != nil {
		t.Errorf("a childless story must have no Family, got %+v", leaf.Family)
	}
	if leaf.Figures.CostUSD != 2.0 || leaf.Figures.FreshInput != 1000 {
		t.Errorf("child figures = %+v, want $2 / 1000 fresh", leaf.Figures)
	}
}
